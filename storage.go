package main

import (
	"context"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sirupsen/logrus"
)

// Thin wrapper for S3 storage client
type BlobStorage struct {
	client *minio.Client
	bucket string
}

func (s *BlobStorage) GetKey(ctx context.Context, key, etag string) (bool, int64, io.ReadCloser, string, string, error) {
	objInfo, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return false, 0, nil, "", "", err
	}
	if etag != "" && etag == objInfo.ETag {
		return false, 0, nil, "", "", nil
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return false, 0, nil, "", "", err
	}
	return true, objInfo.Size, obj, objInfo.ContentType, objInfo.ETag, nil
}

func (s *BlobStorage) WriteKey(ctx context.Context, key string, src io.Reader, size int64, mime string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, src, size, minio.PutObjectOptions{ContentType: mime})
	return err
}

func (s *BlobStorage) DelKey(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
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

	return &BlobStorage{client: minioClient, bucket: bucket}
}
