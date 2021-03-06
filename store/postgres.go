package store

import (
	log "github.com/Sirupsen/logrus"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"sync"
	"time"
)

type Postgres struct {
	db              *sqlx.DB
	expireChan      chan expireReq
	expirys         map[string]int64
	expireMut       *sync.Mutex
	expireInterval  int64
	expireThreshold int
}

type expireReq struct {
	timestamp  int64
	customerId string
}

func NewPostgres(connection string, expireInterval, expireThreshold int) (Store, error) {
	db, err := sqlx.Open("postgres", connection)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(64)
	db.SetMaxIdleConns(8)

	return &Postgres{
		db:              db,
		expireChan:      make(chan expireReq),
		expirys:         make(map[string]int64),
		expireInterval:  int64(expireInterval),
		expireThreshold: expireThreshold,
		expireMut:       &sync.Mutex{},
	}, nil
}

func (pg *Postgres) Start() {
	log.Info("starting db expiry channel")

	for req := range pg.expireChan {
		lastEx, ok := pg.expirys[req.customerId]
		if !ok {
			pg.expirys[req.customerId] = req.timestamp
			continue
		}

		if req.timestamp-lastEx > pg.expireInterval {
			go func(req expireReq) {
				logger := log.WithFields(log.Fields{
					"customer-id": req.customerId,
					"timestamp":   req.timestamp,
				})

				err := pg.expireEntities(req.customerId, req.timestamp)
				if err != nil {
					logger.WithError(err).Error("error expiring entities")
					return
				}

				logger.Info("expiring entities")

				pg.expireMut.Lock()
				defer pg.expireMut.Unlock()
				pg.expirys[req.customerId] = req.timestamp
			}(req)
		}
	}
}

func (pg *Postgres) PutEntity(entity interface{}) (*EntityResponse, error) {
	var (
		err        error
		response   *EntityResponse
		customerId string
	)

	switch entity.(type) {
	case *Instance:
		err = pg.putInstance(entity.(*Instance))
		response = &EntityResponse{entity}
		customerId = entity.(*Instance).CustomerId

	case *Group:
		err = pg.putGroup(entity.(*Group))
		response = &EntityResponse{entity}
		customerId = entity.(*Group).CustomerId

	case *RouteTable:
		err = pg.putRouteTable(entity.(*RouteTable))
		response = &EntityResponse{entity}
		customerId = entity.(*RouteTable).CustomerId

	case *Subnet:
		err = pg.putSubnet(entity.(*Subnet))
		response = &EntityResponse{entity}
		customerId = entity.(*Subnet).CustomerId
	}

	if err == nil {
		lastSync := time.Now()
		customer := &Customer{Id: customerId, LastSync: lastSync}
		err = pg.putCustomer(customer)
		if err != nil {
			return nil, err
		}

		pg.expireChan <- expireReq{lastSync.Unix(), customerId}
	}

	return response, err
}

func (pg *Postgres) GetInstance(request *InstanceRequest) (*InstanceResponse, error) {
	if request.CustomerId == "" {
		return nil, ErrMissingCustomerId
	}

	if request.InstanceId == "" {
		return nil, ErrMissingInstanceId
	}

	instance := new(Instance)
	err := pg.db.Get(instance, "select * from instances where customer_id = $1 and id = $2", request.CustomerId, request.InstanceId)
	return &InstanceResponse{instance}, err
}

func (pg *Postgres) ListInstances(request *InstancesRequest) (*InstancesResponse, error) {
	instances, err := pg.listInstances(request)
	if err != nil {
		return nil, err
	}

	responses := make([]*InstanceResponse, len(instances))
	for i, inst := range instances {
		responses[i] = &InstanceResponse{inst}
	}

	return &InstancesResponse{responses}, err
}

func (pg *Postgres) CountInstances(request *InstancesRequest) (*CountResponse, error) {
	if request.CustomerId == "" {
		return nil, ErrMissingCustomerId
	}

	var err error
	var count int

	if request.Type == "" {
		err = pg.db.Get(&count, "select count(id) from instances where customer_id = $1", request.CustomerId)
	} else {
		err = pg.db.Get(&count, "select count(id) from instances where customer_id = $1 and type = $2", request.CustomerId, request.Type)
	}

	return &CountResponse{count}, err
}

func (pg *Postgres) DeleteInstances() error {
	_, err := pg.db.Exec("delete from instances")
	return err
}

func (pg *Postgres) GetGroup(request *GroupRequest) (*GroupResponse, error) {
	if request.CustomerId == "" {
		return nil, ErrMissingCustomerId
	}

	if request.GroupId == "" {
		return nil, ErrMissingGroupId
	}

	group := new(Group)
	err := pg.db.Get(group, "select * from groups where customer_id = $1 and name = $2", request.CustomerId, request.GroupId)
	if err != nil {
		return nil, err
	}

	instances, err := pg.listInstances(&InstancesRequest{CustomerId: request.CustomerId, GroupId: request.GroupId, Type: request.Type})
	if err != nil {
		return nil, err
	}

	iresponses := make([]*InstanceResponse, len(instances))
	for i, inst := range instances {
		iresponses[i] = &InstanceResponse{inst}
	}

	return &GroupResponse{group, iresponses, len(instances)}, err
}

func (pg *Postgres) ListGroups(request *GroupsRequest) (*GroupsResponse, error) {
	if request.CustomerId == "" {
		return nil, ErrMissingCustomerId
	}

	var err error
	groups := make([]*Group, 0)

	if request.Type == "" {
		err = pg.db.Select(&groups, "select groups.*, count(distinct(groups_instances.instance_id)) as instance_count from groups left outer join groups_instances on groups_instances.group_name = groups.name and groups_instances.customer_id = groups.customer_id where groups.customer_id = $1 group by groups.name, groups.customer_id", request.CustomerId)
	} else {
		err = pg.db.Select(&groups, "select groups.*, count(distinct(groups_instances.instance_id)) as instance_count from groups left outer join groups_instances on groups_instances.group_name = groups.name and groups_instances.customer_id = groups.customer_id where groups.customer_id = $1 and groups.type = $2 group by groups.name, groups.customer_id", request.CustomerId, request.Type)
	}

	if err != nil {
		return nil, err
	}

	grouprs := make([]*GroupResponse, len(groups))
	for i, g := range groups {
		grouprs[i] = &GroupResponse{
			Group:         g,
			InstanceCount: g.InstanceCount,
		}
	}

	return &GroupsResponse{grouprs}, nil
}

func (pg *Postgres) CountGroups(request *GroupsRequest) (*CountResponse, error) {
	if request.CustomerId == "" {
		return nil, ErrMissingCustomerId
	}

	var err error
	var count int

	if request.Type == "" {
		err = pg.db.Get(&count, "select count(name) from groups where customer_id = $1", request.CustomerId)
	} else {
		err = pg.db.Get(&count, "select count(name) from groups where customer_id = $1 and type = $2", request.CustomerId, request.Type)
	}

	return &CountResponse{count}, err
}

func (pg *Postgres) DeleteGroups() error {
	_, err := pg.db.Exec("delete from groups")
	return err
}

func (pg *Postgres) GetCustomer(request *CustomerRequest) (*CustomerResponse, error) {
	if request.Id == "" {
		return nil, ErrMissingCustomerId
	}

	customer := new(Customer)
	err := pg.db.Get(customer, "select * from customers where id = $1", request.Id)

	return &CustomerResponse{customer}, err
}

func (pg *Postgres) listInstances(request *InstancesRequest) ([]*Instance, error) {
	if request.CustomerId == "" {
		return nil, ErrMissingCustomerId
	}

	var err error
	instances := make([]*Instance, 0)

	if request.GroupId != "" {
		err = pg.db.Select(&instances, "select * from instances where customer_id = $1 and id in (select instance_id from groups_instances where customer_id = $1 and group_name = $2)", request.CustomerId, request.GroupId)
	} else if request.Type == "" {
		err = pg.db.Select(&instances, "select * from instances where customer_id = $1", request.CustomerId)
	} else {
		err = pg.db.Select(&instances, "select * from instances where customer_id = $1 and type = $2", request.CustomerId, request.Type)
	}

	return instances, err
}

func (pg *Postgres) putInstance(instance *Instance) error {
	query := "with update_instances as (update instances set (type, data) = (:type, :data) where id = :id and customer_id = :customer_id returning id), insert_instances as (insert into instances (id, customer_id, type, data) select :id as id, :customer_id as customer_id, :type as type, :data as data where not exists (select id from update_instances limit 1) returning id) select * from update_instances union all select * from insert_instances;"
	_, err := pg.db.NamedExec(query, instance)
	if err != nil {
		return err
	}

	// i don't really want to use transactions for this right now until a refactor
	for _, group := range instance.Groups {
		err := pg.ensureGroup(group)
		if err != nil {
			return err
		}

		_, err = pg.db.Exec("insert into groups_instances (customer_id, group_name, instance_id) select $1 as customer_id, ($2::varchar(128)) as group_name, ($3::varchar(128)) as instance_id where not exists (select instance_id from groups_instances where customer_id = $1 and group_name = $2 and instance_id = $3)", group.CustomerId, group.Name, instance.Id)
		if err != nil {
			return err
		}
	}

	return nil
}

func (pg *Postgres) putGroup(group *Group) error {
	query := "with update_groups as (update groups set (type, data) = (:type, :data) where name = :name and customer_id = :customer_id returning name), insert_groups as (insert into groups (name, customer_id, type, data) select :name as name, :customer_id as customer_id, :type as type, :data as data where not exists (select name from update_groups limit 1) returning name) select * from update_groups union all select * from insert_groups;"
	_, err := pg.db.NamedExec(query, group)
	if err != nil {
		return err
	}

	// i don't really want to use transactions for this right now until a refactor
	for _, instance := range group.Instances {
		err := pg.ensureInstance(instance)
		if err != nil {
			return err
		}

		_, err = pg.db.Exec("insert into groups_instances (customer_id, group_name, instance_id) select $1 as customer_id, ($2::varchar(128)) as group_name, ($3::varchar(128)) as instance_id where not exists (select instance_id from groups_instances where customer_id = $1 and group_name = $2 and instance_id = $3)", group.CustomerId, group.Name, instance.Id)
		if err != nil {
			return err
		}
	}

	return nil
}

func (pg *Postgres) putCustomer(customer *Customer) error {
	query := "with update_customers as (update customers set last_sync = :last_sync where id = :id returning id), insert_customers as (insert into customers (id, last_sync) select :id as id, :last_sync as last_sync where not exists (select id from update_customers limit 1) returning id) select * from update_customers union all select * from insert_customers;"
	_, err := pg.db.NamedExec(query, customer)
	return err
}

func (pg *Postgres) putRouteTable(routeTable *RouteTable) error {
	query := `with update_route_tables as
		  (update route_tables set data = :data where customer_id = :customer_id and id = :id returning id),
		  insert_route_tables as (insert into route_tables (id, customer_id, data) select :id as id,
		  :customer_id as customer_id, :data as data where not exists (select id from update_route_tables limit 1) returning id)
		  select * from update_route_tables union all select * from insert_route_tables;
		  `
	_, err := pg.db.NamedExec(query, routeTable)
	return err
}

func (pg *Postgres) putSubnet(subnet *Subnet) error {
	query := `with update_subnets as
		  (update subnets set data = :data where customer_id = :customer_id and id = :id returning id),
		  insert_subnets as (insert into subnets (id, customer_id, data) select :id as id,
		  :customer_id as customer_id, :data as data where not exists (select id from update_subnets limit 1) returning id)
		  select * from update_subnets union all select * from insert_subnets;
		  `
	_, err := pg.db.NamedExec(query, subnet)
	return err
}

func (pg *Postgres) expireEntities(customerId string, lastSync int64) error {
	lastSyncTime := time.Unix(lastSync, 0).Add(time.Duration(-1*pg.expireThreshold) * time.Second)

	groupExpireQuery := "delete from groups where customer_id = $1 and updated_at < $2"
	_, err := pg.db.Exec(groupExpireQuery, customerId, lastSyncTime)
	if err != nil {
		return err
	}

	instanceExpireQuery := "delete from instances where customer_id = $1 and updated_at < $2"
	_, err = pg.db.Exec(instanceExpireQuery, customerId, lastSyncTime)

	return err
}

func (pg *Postgres) ensureInstance(instance *Instance) error {
	_, err := pg.db.Exec("insert into instances (id, customer_id, type, data) select ($1::varchar(128)) as id, $2 as customer_id, $3 as type, $4 as data where not exists (select id from instances where id = $1 and customer_id = $2)", instance.Id, instance.CustomerId, instance.Type, instance.Data)
	return err
}

func (pg *Postgres) ensureGroup(group *Group) error {
	_, err := pg.db.Exec("insert into groups (name, customer_id, type, data) select ($1::varchar(128)) as name, $2 as customer_id, $3 as type, $4 as data where not exists (select name from groups where name = $1 and customer_id = $2)", group.Name, group.CustomerId, group.Type, group.Data)
	return err
}
