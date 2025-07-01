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
	ctx, stop := getStopCtx()
	defer stop()

	log := SetupLogger()

	localizer, locales := SetupLocales(log)

	storage := SetupBlobStorage(log)
	db := SetupDB(ctx, storage, log)
	g, srv := SetupServer(log, localizer)
	names := SetupProviders(log)
	SetupRoutes(g, ctx, storage, db, log, names, locales, localizer)

	var wg sync.WaitGroup
	RunServer(srv, &wg, ctx, log)

	<-ctx.Done()
	log.Info("Shutdown signal received")
	wg.Wait()

	log.Info("All groutines are finished")
}
