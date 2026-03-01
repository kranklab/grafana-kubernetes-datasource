package plugin

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
)

// eksEnv reads a required env var and skips the test if it is unset or empty.
func eksEnv(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("skipping: %s not set (see .env.test)", key)
	}
	return v
}

// eksEnvOpt reads an optional env var (returns "" if unset).
func eksEnvOpt(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// baseData builds a jsonData with the common EKS fields set.
// URL and CA cert are fetched automatically via DescribeCluster.
func baseData(t *testing.T) jsonData {
	t.Helper()
	return jsonData{
		CertInputMode:  "aws",
		AwsRegion:      eksEnv(t, "TEST_AWS_REGION"),
		EksClusterName: eksEnv(t, "TEST_EKS_CLUSTER_NAME"),
	}
}

func inspectToken(t *testing.T, token string, clusterName string) {
	t.Helper()
	encoded := strings.TrimPrefix(token, "k8s-aws-v1.")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Logf("could not decode token: %v", err)
		return
	}
	presignedURL := string(decoded)
	t.Logf("presigned URL: %s", presignedURL)
	if strings.Contains(presignedURL, "x-k8s-aws-id") {
		t.Logf("x-k8s-aws-id IS in SignedHeaders")
	} else {
		t.Logf("WARNING: x-k8s-aws-id NOT found in presigned URL")
	}

	// Call the presigned URL directly to verify STS accepts it
	req, _ := http.NewRequest("GET", presignedURL, nil)
	req.Header.Set("x-k8s-aws-id", clusterName)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("STS call failed: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("STS response %d: %s", resp.StatusCode, body)
}

// TestEKSIAMUser verifies the full chain for static IAM user credentials:
//  1. AWS credentials are accepted by STS (GetCallerIdentity)
//  2. A well-formed EKS token is generated
//  3. The token can connect to the cluster (ServerVersion)
func TestEKSIAMUser(t *testing.T) {
	data := baseData(t)
	data.AwsIamMode = "user"

	secrets := map[string]string{
		"awsAccessKeyId":     eksEnv(t, "TEST_AWS_ACCESS_KEY_ID"),
		"awsSecretAccessKey": eksEnv(t, "TEST_AWS_SECRET_ACCESS_KEY"),
	}

	ctx := context.Background()

	t.Run("build AWS config", func(t *testing.T) {
		_, err := buildAWSConfig(ctx, data, secrets)
		if err != nil {
			t.Fatalf("buildAWSConfig: %v", err)
		}
	})

	t.Run("generate EKS token", func(t *testing.T) {
		cfg, err := buildAWSConfig(ctx, data, secrets)
		if err != nil {
			t.Fatalf("buildAWSConfig: %v", err)
		}
		token, err := generateEKSToken(ctx, data.EksClusterName, cfg)
		if err != nil {
			t.Fatalf("generateEKSToken: %v", err)
		}
		if !strings.HasPrefix(token, "k8s-aws-v1.") {
			t.Fatalf("unexpected token prefix: %q", token[:min(len(token), 20)])
		}
		t.Logf("token prefix OK, length=%d", len(token))
		inspectToken(t, token, data.EksClusterName)
	})

	t.Run("kubernetes connectivity", func(t *testing.T) {
		kubeCfg, err := buildKubeConfig(ctx, data, secrets)
		if err != nil {
			t.Fatalf("buildKubeConfig: %v", err)
		}
		clientset, err := kubernetes.NewForConfig(kubeCfg)
		if err != nil {
			t.Fatalf("NewForConfig: %v", err)
		}
		version, err := clientset.Discovery().ServerVersion()
		if err != nil {
			t.Fatalf("ServerVersion: %v", err)
		}
		t.Logf("connected: %s", version.GitVersion)
	})
}

// TestEKSIAMRole verifies assume-role auth. If no access key vars are set,
// the default credential chain is used as the base (instance profile / env).
func TestEKSIAMRole(t *testing.T) {
	data := baseData(t)
	data.AwsIamMode = "role"
	data.AwsRoleArn = eksEnv(t, "TEST_AWS_ROLE_ARN")
	data.AwsExternalId = eksEnvOpt("TEST_AWS_EXTERNAL_ID")

	secrets := map[string]string{
		"awsAccessKeyId":     eksEnvOpt("TEST_AWS_ACCESS_KEY_ID"),
		"awsSecretAccessKey": eksEnvOpt("TEST_AWS_SECRET_ACCESS_KEY"),
	}

	ctx := context.Background()

	t.Run("assume role", func(t *testing.T) {
		_, err := buildAWSConfig(ctx, data, secrets)
		if err != nil {
			t.Fatalf("buildAWSConfig (role): %v", err)
		}
	})

	t.Run("generate EKS token", func(t *testing.T) {
		cfg, err := buildAWSConfig(ctx, data, secrets)
		if err != nil {
			t.Fatalf("buildAWSConfig: %v", err)
		}
		token, err := generateEKSToken(ctx, data.EksClusterName, cfg)
		if err != nil {
			t.Fatalf("generateEKSToken: %v", err)
		}
		if !strings.HasPrefix(token, "k8s-aws-v1.") {
			t.Fatalf("unexpected token prefix: %q", token[:min(len(token), 20)])
		}
		t.Logf("token prefix OK, length=%d", len(token))
		inspectToken(t, token, data.EksClusterName)
	})

	t.Run("kubernetes connectivity", func(t *testing.T) {
		kubeCfg, err := buildKubeConfig(ctx, data, secrets)
		if err != nil {
			t.Fatalf("buildKubeConfig: %v", err)
		}
		clientset, err := kubernetes.NewForConfig(kubeCfg)
		if err != nil {
			t.Fatalf("NewForConfig: %v", err)
		}
		version, err := clientset.Discovery().ServerVersion()
		if err != nil {
			t.Fatalf("ServerVersion: %v", err)
		}
		t.Logf("connected: %s", version.GitVersion)
	})
}
