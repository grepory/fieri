package api

import (
	"encoding/json"
	"fmt"
	"github.com/go-kit/kit/endpoint"
	kvlog "github.com/go-kit/kit/log"
	httptransport "github.com/go-kit/kit/transport/http"
	"github.com/opsee/fieri/store"
	"golang.org/x/net/context"
	"net/http"
)

type errorResponse struct {
	Message string `json:"message"`
}

type countResponse struct {
	Count int `json:"count"`
}

var serverOptions = []httptransport.ServerOption{
	httptransport.ServerAfter(httptransport.SetContentType("application/json")),
	httptransport.ServerErrorEncoder(encodeError),
}

func Start(addr string, db store.Store, logger kvlog.Logger) error {
	ctx := context.Background()

	http.Handle("/instances", makeHandler(ctx, logger, "/instances", makeInstancesEndpoint(db)))
	http.Handle("/groups", makeHandler(ctx, logger, "/groups", makeGroupsEndpoint(db)))
	http.Handle("/instances/count", makeHandler(ctx, logger, "/instances/count", makeInstancesCountEndpoint(db)))
	http.Handle("/groups/count", makeHandler(ctx, logger, "/groups/count", makeGroupsCountEndpoint(db)))

	return http.ListenAndServe(addr, nil)
}

func makeHandler(ctx context.Context, logger kvlog.Logger, path string, ep endpoint.Endpoint) *httptransport.Server {
	return httptransport.NewServer(
		ctx,
		loggingMiddleware(kvlog.NewContext(logger).With("path", path))(ep),
		decodeRequest,
		encodeResponse,
		serverOptions...,
	)
}

func loggingMiddleware(logger kvlog.Logger) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, request interface{}) (interface{}, error) {
			logger.Log("request", fmt.Sprintf("%#v", request))
			return next(ctx, request)
		}
	}
}

func encodeResponse(w http.ResponseWriter, response interface{}) error {
	return json.NewEncoder(w).Encode(response)
}

func encodeError(w http.ResponseWriter, err error) {
	json.NewEncoder(w).Encode(errorResponse{err.Error()})
}

func decodeRequest(r *http.Request) (interface{}, error) {
	request := &store.Options{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return nil, err
	}
	return request, nil
}
func makeInstancesEndpoint(db store.Store) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(*store.Options)

		instances, err := db.ListInstances(req)
		if err != nil {
			return nil, err
		}

		return instances, nil
	}
}

func makeGroupsEndpoint(db store.Store) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(*store.Options)

		groups, err := db.ListGroups(req)
		if err != nil {
			return nil, err
		}

		return groups, nil
	}
}

func makeInstancesCountEndpoint(db store.Store) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(*store.Options)
		count, err := db.CountInstances(req)

		if err != nil {
			return nil, err
		}

		return countResponse{count}, nil
	}
}

func makeGroupsCountEndpoint(db store.Store) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(*store.Options)
		count, err := db.CountGroups(req)

		if err != nil {
			return nil, err
		}

		return countResponse{count}, nil
	}
}
