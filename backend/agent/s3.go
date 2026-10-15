package agent

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Blobs reads uploaded screenshots and issues presigned URLs for the UI.
type S3Blobs struct {
	Client *s3.Client
	Bucket string
}

func (b *S3Blobs) Get(ctx context.Context, key string) ([]byte, string, error) {
	out, err := b.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, "", err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(io.LimitReader(out.Body, 20<<20))
	return data, aws.ToString(out.ContentType), err
}

func (b *S3Blobs) PresignPut(ctx context.Context, key, contentType string) (string, error) {
	p := s3.NewPresignClient(b.Client)
	out, err := p.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.Bucket), Key: aws.String(key), ContentType: aws.String(contentType)}, s3.WithPresignExpires(10*time.Minute))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

func (b *S3Blobs) PresignGet(ctx context.Context, key string) (string, error) {
	p := s3.NewPresignClient(b.Client)
	out, err := p.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.Bucket), Key: aws.String(key)}, s3.WithPresignExpires(10*time.Minute))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

// Wipe deletes every object in the uploads bucket (household reset).
func (b *S3Blobs) Wipe(ctx context.Context) error {
	var next *string
	for {
		out, err := b.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(b.Bucket), ContinuationToken: next})
		if err != nil {
			return err
		}
		if len(out.Contents) > 0 {
			ids := make([]s3types.ObjectIdentifier, 0, len(out.Contents))
			for _, o := range out.Contents {
				ids = append(ids, s3types.ObjectIdentifier{Key: o.Key})
			}
			res, err := b.Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(b.Bucket), Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)}})
			if err != nil {
				return err
			}
			if n := len(res.Errors); n > 0 {
				return fmt.Errorf("%d objects could not be deleted: %s", n, aws.ToString(res.Errors[0].Message))
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return nil
		}
		next = out.NextContinuationToken
	}
}
