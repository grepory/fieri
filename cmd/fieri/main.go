package main

import (
	log "github.com/Sirupsen/logrus"

	"github.com/opsee/fieri/consumer"
	"github.com/opsee/fieri/service"
	"github.com/opsee/fieri/store"
	"github.com/yeller/yeller-golang"
	"os"
	"os/signal"
	"strings"
)

func main() {
	yeller.StartWithErrorHandlerEnvApplicationRoot(os.Getenv("YELLER_KEY"), "production", "/build/src/github.com/opsee/fieri", yeller.NewSilentErrorHandler())

	pgConnection := os.Getenv("POSTGRES_CONN")
	if pgConnection == "" {
		log.Fatal("You have to give me a postgres connection by setting the POSTGRES_CONN env var")
	}

	db, err := store.NewPostgres(pgConnection, 60, 120)
	if err != nil {
		log.Fatal("Error initializing postgres:", err)
	}

	lookupdHosts := os.Getenv("LOOKUPD_HOSTS")
	if lookupdHosts == "" {
		log.Fatal("You'll need to give me a nsqlookupd connection(s) by setting the LOOKUPD_HOSTS env var (comma-separated)")
	}

	bastionDiscoveryTopic := os.Getenv("BASTION_DISCOVERY_TOPIC")
	if bastionDiscoveryTopic == "" {
		log.Fatal("You have to give me a topic to consume by setting the BASTION_DISCOVERY_TOPIC env var")
	}

	lookupds := strings.Split(lookupdHosts, ",")
	nsqConsumer, err := consumer.NewNsq(lookupds, db, bastionDiscoveryTopic)
	if err != nil {
		log.Fatal("Error initializing nsq consumer:", err)
	}

	addr := os.Getenv("FIERI_HTTP_ADDR")
	if addr == "" {
		log.Fatal("You have to give me a listening address by setting the FIERI_HTTP_ADDR env var")
	}

	service := service.NewService(db)
	go service.StartHTTP(addr)
	go db.Start()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill)
	<-interrupt

	nsqConsumer.Stop()
}
