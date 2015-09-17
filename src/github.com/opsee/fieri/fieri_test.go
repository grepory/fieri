package fieri

import (
	"encoding/json"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/nsqio/go-nsq"
	"github.com/opsee/fieri/consumer"
	"github.com/opsee/fieri/store"
	"github.com/stretchr/testify/suite"
	"os"
	"strings"
	"testing"
	"time"
)

const testCustomerId = "a8a20324-57db-11e5-88a1-37e8cfb78836"
const testTopic = "discovery"

type TestSuite struct {
	suite.Suite
	Store             store.Store
	Producer          *nsq.Producer
	Consumer          consumer.Consumer
	Instances         []*ec2.Instance
	SecurityGroups    []*ec2.SecurityGroup
	LoadBalancers     []*elb.LoadBalancerDescription
	RdsInstances      []*rds.DBInstance
	RdsSecurityGroups []*rds.DBSecurityGroup
}

func (suite *TestSuite) SetupSuite() {
	t := suite.T()

	suite.Store = setupDb(t)
	suite.Producer = setupProducer(t)
	suite.Instances = loadInstances(t)
	suite.SecurityGroups = loadSecurityGroups(t)
	suite.LoadBalancers = loadLoadBalancers(t)
	suite.RdsInstances = loadRdsInstances(t)
	suite.RdsSecurityGroups = loadRdsSecurityGroups(t)

	suite.Store.DeleteInstances()
	suite.Store.DeleteGroups()
}

func (suite *TestSuite) TearDownSuite() {
	time.Sleep(50 * time.Millisecond)
	suite.Producer.Stop()
	time.Sleep(50 * time.Millisecond)
	suite.Consumer.Stop()
}

func (suite *TestSuite) TestInstances() {
	for _, inst := range suite.Instances {
		publishEvent(suite.Producer, "Instance", inst)
	}
	setupConsumer(suite)
	time.Sleep(500 * time.Millisecond)
	instances, _ := suite.Store.ListInstances(&store.Options{CustomerId: testCustomerId, Type: "ec2"})
	suite.Equal(len(instances), len(suite.Instances))
}

func (suite *TestSuite) TestDbInstances() {
	for _, inst := range suite.RdsInstances {
		publishEvent(suite.Producer, "DBInstance", inst)
	}
	setupConsumer(suite)
	time.Sleep(500 * time.Millisecond)
	instances, _ := suite.Store.ListInstances(&store.Options{CustomerId: testCustomerId, Type: "rds"})
	suite.Equal(len(suite.RdsInstances), len(instances))
}

func (suite *TestSuite) TestSecurityGroups() {
	for _, group := range suite.SecurityGroups {
		publishEvent(suite.Producer, "SecurityGroup", group)
	}
	setupConsumer(suite)
	time.Sleep(500 * time.Millisecond)
	groups, _ := suite.Store.ListGroups(&store.Options{CustomerId: testCustomerId, Type: "security"})
	suite.Equal(len(suite.SecurityGroups), len(groups))
}

func (suite *TestSuite) TestELBGroups() {
	for _, group := range suite.LoadBalancers {
		publishEvent(suite.Producer, "LoadBalancerDescription", group)
	}
	setupConsumer(suite)
	time.Sleep(500 * time.Millisecond)
	groups, _ := suite.Store.ListGroups(&store.Options{CustomerId: testCustomerId, Type: "elb"})
	suite.Equal(len(suite.LoadBalancers), len(groups))
}

func (suite *TestSuite) TestDbSecurityGroups() {
	for _, group := range suite.RdsSecurityGroups {
		publishEvent(suite.Producer, "DBSecurityGroup", group)
	}
	setupConsumer(suite)
	time.Sleep(500 * time.Millisecond)
	groups, _ := suite.Store.ListGroups(&store.Options{CustomerId: testCustomerId, Type: "rds-security"})
	suite.Equal(len(suite.RdsSecurityGroups), len(groups))
}

func TestTestSuite(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func publishEvent(producer *nsq.Producer, messageType string, message interface{}) {
	msg, _ := json.Marshal(message)
	event := &consumer.Event{
		CustomerId:  testCustomerId,
		MessageType: messageType,
		MessageBody: string(msg),
	}
	eventBytes, _ := json.Marshal(event)
	producer.Publish(testTopic, eventBytes)
}

func setupDb(t *testing.T) store.Store {
	db, err := store.NewPostgres(os.Getenv("POSTGRES_CONN"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func setupConsumer(suite *TestSuite) {
	if suite.Consumer == nil {
		nsq, err := consumer.NewNsq(strings.Split(os.Getenv("LOOKUPD_HOSTS"), ","), suite.Store, nil, 1, testTopic)
		if err != nil {
			suite.T().Fatal(err)
		}
		suite.Consumer = nsq
	}
}

func setupProducer(t *testing.T) *nsq.Producer {
	nsqdHost := os.Getenv("NSQD_HOST")
	if nsqdHost == "" {
		t.Fatal("error connecting to nsqd, you need to set NSQD_HOST")
	}

	config := nsq.NewConfig()
	producer, err := nsq.NewProducer(nsqdHost, config)
	if err != nil {
		t.Fatal("error connecting to nsqd: ", err)
	}

	return producer
}

func loadRdsSecurityGroups(t *testing.T) []*rds.DBSecurityGroup {
	var rdsSecurityGroupsJson struct {
		DBSecurityGroups []*rds.DBSecurityGroup
	}

	err := readJson("fixtures/db-security-groups.json", &rdsSecurityGroupsJson)
	if err != nil {
		t.Fatal(err)
	}

	return rdsSecurityGroupsJson.DBSecurityGroups
}

func loadRdsInstances(t *testing.T) []*rds.DBInstance {
	var rdsInstancesJson struct {
		DBInstances []*rds.DBInstance
	}

	err := readJson("fixtures/db-instances.json", &rdsInstancesJson)
	if err != nil {
		t.Fatal(err)
	}

	return rdsInstancesJson.DBInstances
}

func loadLoadBalancers(t *testing.T) []*elb.LoadBalancerDescription {
	var loadBalancersJson struct {
		LoadBalancerDescriptions []*elb.LoadBalancerDescription
	}

	err := readJson("fixtures/load-balancers.json", &loadBalancersJson)
	if err != nil {
		t.Fatal(err)
	}

	return loadBalancersJson.LoadBalancerDescriptions
}

func loadSecurityGroups(t *testing.T) []*ec2.SecurityGroup {
	var securityGroupsJson struct {
		SecurityGroups []*ec2.SecurityGroup
	}

	err := readJson("fixtures/security-groups.json", &securityGroupsJson)
	if err != nil {
		t.Fatal(err)
	}

	return securityGroupsJson.SecurityGroups
}

func loadInstances(t *testing.T) []*ec2.Instance {
	var instancesJson struct {
		Reservations *[]struct {
			Instances []*ec2.Instance
		}
	}

	err := readJson("fixtures/instances.json", &instancesJson)
	if err != nil {
		t.Fatal(err)
	}

	// flatmap the instances
	instances := make([]*ec2.Instance, len(*instancesJson.Reservations))
	for i, r := range *instancesJson.Reservations {
		instances[i] = r.Instances[0]
	}

	return instances
}

func readJson(filePath string, thing interface{}) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(thing)

	return err
}
