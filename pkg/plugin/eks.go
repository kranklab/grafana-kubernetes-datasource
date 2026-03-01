package plugin

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// buildAWSConfig constructs an AWS config using either static IAM user credentials
// or an assumed role. If no access key is provided for role mode, the default
// credential chain (env vars, instance profile, etc.) is used as the base.
func buildAWSConfig(ctx context.Context, data jsonData, secrets map[string]string) (aws.Config, error) {
	accessKeyID := strings.TrimSpace(secrets["awsAccessKeyId"])
	secretKey := strings.TrimSpace(secrets["awsSecretAccessKey"])

	baseOpts := []func(*config.LoadOptions) error{
		config.WithRegion(data.AwsRegion),
	}
	if accessKeyID != "" {
		baseOpts = append(baseOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, baseOpts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("loading AWS config: %w", err)
	}

	if data.AwsIamMode == "role" && data.AwsRoleArn != "" {
		stsClient := sts.NewFromConfig(cfg)
		roleProvider := stscreds.NewAssumeRoleProvider(stsClient, data.AwsRoleArn, func(o *stscreds.AssumeRoleOptions) {
			if data.AwsExternalId != "" {
				o.ExternalID = aws.String(data.AwsExternalId)
			}
		})
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(data.AwsRegion),
			config.WithCredentialsProvider(aws.NewCredentialsCache(roleProvider)),
		)
		if err != nil {
			return aws.Config{}, fmt.Errorf("loading AWS config for role: %w", err)
		}
	}

	return cfg, nil
}

// describeEKSCluster fetches the cluster API endpoint and CA certificate data
// from the EKS API. This avoids requiring users to copy these values manually.
func describeEKSCluster(ctx context.Context, clusterName string, cfg aws.Config) (endpoint string, caData []byte, err error) {
	result, err := eks.NewFromConfig(cfg).DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return "", nil, fmt.Errorf("DescribeCluster: %w", err)
	}
	c := result.Cluster
	if c.Endpoint == nil {
		return "", nil, fmt.Errorf("cluster endpoint is empty")
	}
	if c.CertificateAuthority == nil || c.CertificateAuthority.Data == nil {
		return "", nil, fmt.Errorf("cluster CA is empty")
	}
	caData, err = base64.StdEncoding.DecodeString(*c.CertificateAuthority.Data)
	if err != nil {
		return "", nil, fmt.Errorf("decoding cluster CA: %w", err)
	}
	return *c.Endpoint, caData, nil
}

// generateEKSToken produces a pre-signed STS GetCallerIdentity token in the
// format expected by the EKS authenticating webhook ("k8s-aws-v1.<base64url>").
func generateEKSToken(ctx context.Context, clusterName string, cfg aws.Config) (string, error) {
	presignClient := sts.NewPresignClient(sts.NewFromConfig(cfg))

	req, err := presignClient.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
		func(opts *sts.PresignOptions) {
			opts.ClientOptions = append(opts.ClientOptions, func(o *sts.Options) {
				o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
					return stack.Build.Add(
						middleware.BuildMiddlewareFunc("EKSClusterHeader",
							func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
								if r, ok := in.Request.(*smithyhttp.Request); ok {
									r.Header.Set("x-k8s-aws-id", clusterName)
									q := r.URL.Query()
									q.Set("X-Amz-Expires", "60")
									r.URL.RawQuery = q.Encode()
								}
								return next.HandleBuild(ctx, in)
							},
						),
						middleware.After,
					)
				})
			})
		},
	)
	if err != nil {
		return "", fmt.Errorf("presigning STS request: %w", err)
	}

	return "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(req.URL)), nil
}
