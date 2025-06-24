package main

import (
	"context"
	"os/signal"
	"sync"
	"syscall"
)

func getStopCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func main() {
	log := SetupLogger()

	storage := SetupBlobStorage(log)
	db := SetupDB(storage, log)
	g, srv := SetupServer(log)

	ctx, stop := getStopCtx()
	defer stop()

	SetupRoutes(g, ctx, storage, db, log)

	var wg sync.WaitGroup
	RunServer(srv, &wg, ctx, log)

	<-ctx.Done()
	log.Info("Shutdown signal received")
	wg.Wait()

	log.Info("All groutines are finished")
}
