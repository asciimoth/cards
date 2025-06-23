package main

func main() {
	log := SetupLogger()
	storage := SetupBlobStorage(log)
	_ = storage
	log.Debug("S3 storage connected")
	db := SetupDB(storage, log)
	_ = db
	log.Debug("DB connected")
}
