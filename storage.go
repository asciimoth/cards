package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sirupsen/logrus"
)

// BlobStorage is a thin S3 wrapper with an in‑memory LRU cache.
type BlobStorage struct {
	client *minio.Client
	bucket string
	prefix string
	cache  *Cache
}

// GetKey fetches an object. If useCache==true it first tries memory.
// Returns (exists, size, reader, err).  If served from cache, reader
// is a bytes.Reader over the cached []byte.
func (s *BlobStorage) GetKey(ctx context.Context, key string, useCache bool) (int64, io.ReadCloser, error) {
	fullKey := s.prefix + key

	if useCache {
		// try cache
		if data, ok := s.cache.Get(fullKey); ok {
			return int64(len(data)), io.NopCloser(bytes.NewReader(data)), nil
		}
	}

	// not in cache or bypass: fetch from S3
	info, err := s.client.StatObject(ctx, s.bucket, fullKey, minio.StatObjectOptions{})
	if err != nil {
		return 0, nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, fullKey, minio.GetObjectOptions{})
	if err != nil {
		return 0, nil, err
	}

	if useCache {
		// read entire payload into memory, fill cache, then return a fresh reader
		buf := make([]byte, info.Size)
		_, err := io.ReadFull(obj, buf)
		obj.Close()
		if err != nil {
			return info.Size, io.NopCloser(bytes.NewReader(buf)), err
		}
		s.cache.Set(fullKey, buf)
		return info.Size, io.NopCloser(bytes.NewReader(buf)), nil
	}

	// bypass cache: return S3 reader directly
	return info.Size, obj, nil
}

// WriteKey writes to S3. If useCache==true, also inserts into cache.
func (s *BlobStorage) WriteKey(ctx context.Context, key string, src io.Reader, size int64, useCache bool) error {
	fullKey := s.prefix + key

	if useCache {
		// buffer will collect everything we send to S3
		var buf bytes.Buffer
		// teeReader writes to buf as it’s read by PutObject
		tee := io.TeeReader(src, &buf)

		// upload from tee, so buf ends up with the full payload
		_, err := s.client.PutObject(ctx, s.bucket, fullKey, tee, size, minio.PutObjectOptions{})
		if err != nil {
			return err
		}

		// now store in cache
		s.cache.Set(fullKey, buf.Bytes())
		return nil
	}

	// bypass cache: stream directly from src
	_, err := s.client.PutObject(ctx, s.bucket, fullKey, src, size, minio.PutObjectOptions{})
	return err
}

// DelKey deletes from S3. If useCache==true, also evicts from cache.
func (s *BlobStorage) DelKey(ctx context.Context, key string) error {
	fullKey := s.prefix + key
	if err := s.client.RemoveObject(ctx, s.bucket, fullKey, minio.RemoveObjectOptions{}); err != nil {
		return err
	}
	return nil
}

func SetupBlobStorage(log *logrus.Logger) *BlobStorage {
	endpoint := os.Getenv("S3_ENDPOINT")

	if endpoint == "" {
		log.Fatal("S3_ENDPOINT not set")
	}

	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")
	bucket := os.Getenv("S3_BUCKET")
	token := os.Getenv("S3_TOKEN")
	useSSL := os.Getenv("S3_USE_SSL") == "true"

	prefix := os.Getenv("S3_PREFIX")

	cache_objects_str := os.Getenv("S3_CACHE_COUNT")
	cache_objects := 100
	cache_memory_str := os.Getenv("S3_CACHE_MEMORY")
	var cache_memory int64 = 5242880

	cache_objects, err := strconv.Atoi(cache_objects_str)
	if err != nil {
		log.Fatalf("Failed to config S3 cache objects count: %s", cache_objects_str)
	}

	cache_memory, err = strconv.ParseInt(cache_memory_str, 10, 64)
	if err != nil {
		log.Fatalf("Failed to config S3 cache size: %s", cache_memory_str)
	}

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, token),
		Secure: useSSL,
	})

	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup blob storage client")
	}

	ok, err := minioClient.BucketExists(context.Background(), bucket)
	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to check if bucket exists")
	}

	if !ok {
		log.Warn("Bucket not exists; Tying to create")
		err := minioClient.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{})
		if err != nil {
			log.WithFields(logrus.Fields{
				"err": err,
			}).Fatal("Failed to create bucket")
		}
	}

	return &BlobStorage{
		client: minioClient,
		bucket: bucket,
		prefix: prefix,
		cache:  NewCache(cache_objects, cache_memory, log),
	}
}
