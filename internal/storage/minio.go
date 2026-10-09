package storage

import (
	"context"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store struct {
	mc     *minio.Client
	bucket string
}

func New(endpoint, user, pass, bucket string, useSSL bool) (*Store, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	return &Store{mc: mc, bucket: bucket}, nil
}

func (s *Store) Bucket() string { return s.bucket }

func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.mc.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return s.mc.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: "us-east-1"})
	}
	return nil
}

// PresignPut returns a short-lived upload URL (15 min).
func (s *Store) PresignPut(ctx context.Context, key string) (string, error) {
	u, err := s.mc.PresignedPutObject(ctx, s.bucket, key, 15*time.Minute)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignGet returns a short-lived download URL (5 min).
func (s *Store) PresignGet(ctx context.Context, key string, inline bool, filename string) (string, error) {
	q := url.Values{}
	if inline {
		q.Set("response-content-disposition", `inline; filename="`+filename+`"`)
	} else {
		q.Set("response-content-disposition", `attachment; filename="`+filename+`"`)
	}
	u, err := s.mc.PresignedGetObject(ctx, s.bucket, key, 5*time.Minute, q)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Store) Stat(ctx context.Context, key string) (int64, string, error) {
	info, err := s.mc.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, "", err
	}
	return info.Size, info.ContentType, nil
}

func (s *Store) Remove(ctx context.Context, key string) error {
	return s.mc.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *Store) Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	_, err := s.mc.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}
