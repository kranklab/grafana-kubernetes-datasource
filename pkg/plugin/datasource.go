package plugin

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"strings"
	"time"
)

// Make sure Datasource implements required interfaces. This is important to do
// since otherwise we will only get a not implemented error response from plugin in
// runtime. In this example datasource instance implements backend.QueryDataHandler,
// backend.CheckHealthHandler interfaces. Plugin should not implement all these
// interfaces - only those which are required for a particular task.
var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// NewDatasource creates a new datasource instance.
func NewDatasource(_ context.Context, _ backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	return &Datasource{}, nil
}

// Datasource is an example datasource which can respond to data queries, reports
// its health and has streaming skills.
type Datasource struct{}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created. As soon as datasource settings change detected by SDK old datasource instance will
// be disposed and a new one will be created using NewSampleDatasource factory function.
func (d *Datasource) Dispose() {
	// Clean up datasource instance resources.
}

// QueryData handles multiple queries and returns multiple responses.
// req contains the queries []DataQuery (where each query contains RefID as a unique identifier).
// The QueryDataResponse contains a map of RefID to the response for each query, and each response
// contains Frames ([]*Frame).
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// create response struct
	response := backend.NewQueryDataResponse()

	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := d.query(ctx, req.PluginContext, q)

		// save the response in a hashmap
		// based on with RefID as identifier
		response.Responses[q.RefID] = res
	}

	return response, nil
}

type queryModel struct {
	Action        string `json:"action"`
	Resource      string `json:"resource"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	LabelSelector string `json:"labelSelector"`
	NodeName      string `json:"nodeName"`
	Container     string `json:"container"`
}

type jsonData struct {
	Url            string `json:"url"`
	ClientCert     string `json:"clientCert"`
	ClientKey      string `json:"clientKey"`
	CaCert         string `json:"caCert"`
	CertInputMode  string `json:"certInputMode"`
	CaCertMode     string `json:"caCertMode"`
	AwsRegion      string `json:"awsRegion"`
	EksClusterName string `json:"eksClusterName"`
	AwsIamMode     string `json:"awsIamMode"`
	AwsRoleArn     string `json:"awsRoleArn"`
	AwsExternalId  string `json:"awsExternalId"`
}

func buildKubeConfig(ctx context.Context, data jsonData, secrets map[string]string) (*rest.Config, error) {
	cfg := &rest.Config{Host: data.Url}
	switch data.CertInputMode {
	case "inline":
		cfg.TLSClientConfig = rest.TLSClientConfig{
			CertData: []byte(secrets["clientCertData"]),
			KeyData:  []byte(secrets["clientKeyData"]),
			CAData:   []byte(secrets["caCertData"]),
		}
	case "token":
		cfg.BearerToken = strings.TrimSpace(secrets["bearerToken"])
		if data.CaCertMode == "inline" {
			cfg.TLSClientConfig = rest.TLSClientConfig{
				CAData: []byte(secrets["caCertData"]),
			}
		} else {
			cfg.TLSClientConfig = rest.TLSClientConfig{
				CAFile: data.CaCert,
			}
		}
	case "aws":
		awsCfg, err := buildAWSConfig(ctx, data, secrets)
		if err != nil {
			return nil, fmt.Errorf("building AWS config: %w", err)
		}
		endpoint, caData, err := describeEKSCluster(ctx, data.EksClusterName, awsCfg)
		if err != nil {
			return nil, fmt.Errorf("describing EKS cluster: %w", err)
		}
		token, err := generateEKSToken(ctx, data.EksClusterName, awsCfg)
		if err != nil {
			return nil, fmt.Errorf("generating EKS token: %w", err)
		}
		cfg.Host = endpoint
		cfg.BearerToken = token
		cfg.TLSClientConfig = rest.TLSClientConfig{CAData: caData}
	default: // "file"
		cfg.TLSClientConfig = rest.TLSClientConfig{
			CertFile: data.ClientCert,
			KeyFile:  data.ClientKey,
			CAFile:   data.CaCert,
		}
	}
	return cfg, nil
}

// CustomObjectReference represents a simplified version of k8s ObjectReference
type CustomObjectReference struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UID       string `json:"uid"`
}

func (d *Datasource) query(ctx context.Context, pCtx backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse

	// Unmarshal the JSON into our queryModel.
	var qm queryModel

	err := json.Unmarshal(query.JSON, &qm)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("json unmarshal: %v", err.Error()))
	}

	switch qm.Action {
	case "summary":
		frame, err := d.runSummaryQuery(ctx, pCtx, qm)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusInternal, fmt.Sprintf("request failed: %v", err.Error()))
		}
		response.Frames = append(response.Frames, frame)
	case "get":
		frames, err := d.runGetQuery(ctx, pCtx, qm)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusInternal, fmt.Sprintf("request failed: %v", err.Error()))
		}
		response.Frames = append(response.Frames, frames...)
	case "list":
		frame, err := d.runListQuery(ctx, pCtx, qm)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusInternal, fmt.Sprintf("request failed: %v", err.Error()))
		}
		response.Frames = append(response.Frames, frame)
	case "logs":
		frame, err := d.runLogsQuery(ctx, pCtx, qm)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusInternal, fmt.Sprintf("request failed: %v", err.Error()))
		}
		response.Frames = append(response.Frames, frame)
	case "yaml":
		frame, err := d.runYAMLQuery(ctx, pCtx, qm)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusInternal, fmt.Sprintf("request failed: %v", err.Error()))
		}
		response.Frames = append(response.Frames, frame)
	}

	return response
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	var jd jsonData
	if err := json.Unmarshal(req.PluginContext.DataSourceInstanceSettings.JSONData, &jd); err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("Invalid configuration: %v", err),
		}, nil
	}

	secrets := req.PluginContext.DataSourceInstanceSettings.DecryptedSecureJSONData

	// For AWS mode, verify IAM credentials before attempting Kubernetes and capture the ARN.
	var iamArn string
	if jd.CertInputMode == "aws" {
		awsCfg, err := buildAWSConfig(ctx, jd, secrets)
		if err != nil {
			return &backend.CheckHealthResult{
				Status:  backend.HealthStatusError,
				Message: fmt.Sprintf("AWS config error: %v", err),
			}, nil
		}
		identity, err := sts.NewFromConfig(awsCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return &backend.CheckHealthResult{
				Status:  backend.HealthStatusError,
				Message: fmt.Sprintf("AWS credentials invalid: %v", err),
			}, nil
		}
		if identity.Arn != nil {
			iamArn = *identity.Arn
		}
	}

	kubeConfig, err := buildKubeConfig(ctx, jd, secrets)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("Failed to build kube config: %v", err),
		}, nil
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("Failed to build Kubernetes client: %v", err),
		}, nil
	}

	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("Cannot reach Kubernetes API: %v", err),
		}, nil
	}

	msg := fmt.Sprintf("Connected to Kubernetes %s", version.GitVersion)
	if iamArn != "" {
		msg += fmt.Sprintf(" (IAM: %s)", iamArn)
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: msg,
	}, nil
}

func (d *Datasource) runListQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (*data.Frame, error) {

	var jd jsonData
	err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &jd)
	if err != nil {
		return nil, err
	}

	secrets := pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData
	kubeConfig, err := buildKubeConfig(ctx, jd, secrets)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	namespace := qm.Namespace
	if namespace == "_all" {
		namespace = ""
	}

	listOpts := metav1.ListOptions{
		LabelSelector: qm.LabelSelector,
	}
	if qm.NodeName != "" && (qm.Resource == "pod" || qm.Resource == "pods") {
		listOpts.FieldSelector = "spec.nodeName=" + qm.NodeName
	}

	switch qm.Resource {
	case "pod", "pods":
		return getPods(ctx, clientset, namespace, listOpts)
	case "deployments":
		return getDeployments(ctx, clientset, namespace, listOpts)
	case "daemonsets":
		return getDeamonSets(ctx, clientset, namespace, listOpts)
	case "statefulsets":
		return getStatefulSets(ctx, clientset, namespace, listOpts)
	case "replicasets":
		return getReplicaSets(ctx, clientset, namespace, listOpts)
	case "jobs":
		return getJobs(ctx, clientset, namespace, listOpts)
	case "cronjobs":
		return getCronJobs(ctx, clientset, namespace, listOpts)
	case "namespaces":
		return getNamespaces(ctx, clientset, listOpts)
	case "services", "svc":
		return getServices(ctx, clientset, namespace, listOpts)
	case "ingresses", "ingress":
		return getIngresses(ctx, clientset, namespace, listOpts)
	case "ingressclasses", "ingressclass":
		return getIngressClasses(ctx, clientset, listOpts)
	case "configmaps", "configmap":
		return getConfigMaps(ctx, clientset, namespace, listOpts)
	case "secrets", "secret":
		return getSecrets(ctx, clientset, namespace, listOpts)
	case "persistentvolumeclaims", "pvc":
		return getPersistentVolumeClaims(ctx, clientset, namespace, listOpts)
	case "storageclasses", "storageclass":
		return getStorageClasses(ctx, clientset, listOpts)
	case "crds", "crd", "customresourcedefinitions":
		return getCRDs(ctx, clientset, listOpts)
	case "clusterrolebindings", "clusterrolebinding":
		return getClusterRoleBindings(ctx, clientset, listOpts)
	case "clusterroles", "clusterrole":
		return getClusterRoles(ctx, clientset, listOpts)
	case "events", "event":
		return getEvents(ctx, clientset, namespace, listOpts)
	case "networkpolicies", "networkpolicy":
		return getNetworkPolicies(ctx, clientset, namespace, listOpts)
	case "nodes", "node":
		return getNodes(ctx, clientset, listOpts)
	case "persistentvolumes", "pv":
		return getPersistentVolumes(ctx, clientset, listOpts)
	case "rolebindings", "rolebinding":
		return getRoleBindings(ctx, clientset, namespace, listOpts)
	case "roles", "role":
		return getRoles(ctx, clientset, namespace, listOpts)
	case "serviceaccounts", "serviceaccount", "sa":
		return getServiceAccounts(ctx, clientset, namespace, listOpts)
	}

	// Fallback: try to discover as a custom resource
	gvr, err := discoverCR(clientset.Discovery(), qm.Resource)
	if err != nil {
		return nil, fmt.Errorf("resource not recognized: %s (%v)", qm.Resource, err)
	}
	dynClient, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}
	return listCustomResources(ctx, dynClient, gvr, namespace, listOpts)
}

func (d *Datasource) runSummaryQuery(ctx context.Context, pCtx backend.PluginContext, _ queryModel) (*data.Frame, error) {
	var jdata jsonData
	err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &jdata)
	if err != nil {
		return nil, err
	}

	secrets := pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData
	kubeConfig, err := buildKubeConfig(ctx, jdata, secrets)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	counts, err := getWorkloadCounts(ctx, clientset)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Total", nil, []int32{}),
		data.NewField("Failed", nil, []int32{}),
	)

	frame.AppendRow("pod", int32(counts.Pods.Total), int32(counts.Pods.Failed))
	frame.AppendRow("deployment", int32(counts.Deployments.Total), int32(counts.Deployments.Failed))
	frame.AppendRow("daemonset", int32(counts.DaemonSets.Total), int32(counts.DaemonSets.Failed))
	frame.AppendRow("statefulset", int32(counts.StatefulSets.Total), int32(counts.StatefulSets.Failed))
	frame.AppendRow("job", int32(counts.Jobs.Total), int32(counts.Jobs.Failed))
	frame.AppendRow("cronjob", int32(counts.CronJobs.Total), int32(counts.CronJobs.Failed))
	frame.AppendRow("replicasets", int32(counts.ReplicaSets.Total), int32(counts.ReplicaSets.Failed))

	return frame, nil
}

func setDetailMeta(frame *data.Frame) {
	frame.Meta = &data.FrameMeta{
		PreferredVisualizationPluginID: "kranklab-kubernetes-panel",
	}
}

func (d *Datasource) runGetQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (data.Frames, error) {
	var jdata jsonData
	if err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &jdata); err != nil {
		return nil, err
	}
	secrets := pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData
	kubeConfig, err := buildKubeConfig(ctx, jdata, secrets)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	namespace := qm.Namespace
	if namespace == "_all" {
		namespace = ""
	}

	switch qm.Resource {
	case "pod", "pods":
		return getPodDetail(ctx, clientset, namespace, qm.Name)
	case "deployments", "deployment":
		return getDeploymentDetail(ctx, clientset, namespace, qm.Name)
	case "daemonsets", "daemonset":
		return getDaemonSetDetail(ctx, clientset, namespace, qm.Name)
	case "statefulsets", "statefulset":
		return getStatefulSetDetail(ctx, clientset, namespace, qm.Name)
	case "replicasets", "replicaset":
		return getReplicaSetDetail(ctx, clientset, namespace, qm.Name)
	case "jobs", "job":
		return getJobDetail(ctx, clientset, namespace, qm.Name)
	case "cronjobs", "cronjob":
		return getCronJobDetail(ctx, clientset, namespace, qm.Name)
	case "services", "service", "svc":
		return getServiceDetail(ctx, clientset, namespace, qm.Name)
	case "ingresses", "ingress":
		return getIngressDetail(ctx, clientset, namespace, qm.Name)
	case "ingressclasses", "ingressclass":
		return getIngressClassDetail(ctx, clientset, qm.Name)
	case "configmaps", "configmap":
		return getConfigMapDetail(ctx, clientset, namespace, qm.Name)
	case "secrets", "secret":
		return getSecretDetail(ctx, clientset, namespace, qm.Name)
	case "persistentvolumeclaims", "pvc":
		return getPVCDetail(ctx, clientset, namespace, qm.Name)
	case "storageclasses", "storageclass":
		return getStorageClassDetail(ctx, clientset, qm.Name)
	case "nodes", "node":
		return getNodeDetailRich(ctx, clientset, qm.Name)
	case "clusterrolebindings", "clusterrolebinding":
		return getClusterRoleBindingDetail(ctx, clientset, qm.Name)
	case "clusterroles", "clusterrole":
		return getClusterRoleDetail(ctx, clientset, qm.Name)
	case "namespaces", "namespace":
		return getNamespaceDetail(ctx, clientset, qm.Name)
	case "networkpolicies", "networkpolicy":
		return getNetworkPolicyDetail(ctx, clientset, namespace, qm.Name)
	case "persistentvolumes", "persistentvolume", "pv":
		return getPersistentVolumeDetail(ctx, clientset, qm.Name)
	case "rolebindings", "rolebinding":
		return getRoleBindingDetail(ctx, clientset, namespace, qm.Name)
	case "roles", "role":
		return getRoleDetail(ctx, clientset, namespace, qm.Name)
	case "serviceaccounts", "serviceaccount", "sa":
		return getServiceAccountDetail(ctx, clientset, namespace, qm.Name)
	case "crds", "crd", "customresourcedefinitions":
		return getCRDDetail(ctx, clientset, qm.Name)
	default:
		gvr, err := discoverCR(clientset.Discovery(), qm.Resource)
		if err != nil {
			return nil, fmt.Errorf("get not supported for resource: %s (%v)", qm.Resource, err)
		}
		dynClient, err := dynamic.NewForConfig(kubeConfig)
		if err != nil {
			return nil, err
		}
		return getCustomResourceDetail(ctx, dynClient, gvr, namespace, qm.Name)
	}
}

func (d *Datasource) runLogsQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (*data.Frame, error) {
	var jdata jsonData
	if err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &jdata); err != nil {
		return nil, err
	}
	secrets := pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData
	kubeConfig, err := buildKubeConfig(ctx, jdata, secrets)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	namespace := qm.Namespace
	if namespace == "_all" || namespace == "" {
		namespace = "default"
	}

	frame := data.NewFrame("logs",
		data.NewField("Timestamp", nil, []time.Time{}),
		data.NewField("Container", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)

	var containers []string
	if qm.Container != "" {
		containers = []string{qm.Container}
	} else {
		pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, qm.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		for _, c := range pod.Spec.Containers {
			containers = append(containers, c.Name)
		}
	}

	tailLines := int64(200)
	for _, containerName := range containers {
		req := clientset.CoreV1().Pods(namespace).GetLogs(qm.Name, &corev1.PodLogOptions{
			Container:  containerName,
			Timestamps: true,
			TailLines:  &tailLines,
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.SplitN(line, " ", 2)
			ts := time.Time{}
			msg := line
			if len(parts) == 2 {
				if t, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
					ts = t
					msg = parts[1]
				}
			}
			frame.AppendRow(ts, containerName, msg)
		}
		_ = stream.Close()
	}

	return frame, nil
}

type resourceInfo struct {
	apiPath    string // e.g. "/api/v1" or "/apis/apps/v1"
	resource   string // e.g. "pods", "deployments"
	namespaced bool
}

var resourceMap = map[string]resourceInfo{
	"pods":                      {"/api/v1", "pods", true},
	"services":                  {"/api/v1", "services", true},
	"configmaps":                {"/api/v1", "configmaps", true},
	"secrets":                   {"/api/v1", "secrets", true},
	"serviceaccounts":           {"/api/v1", "serviceaccounts", true},
	"persistentvolumeclaims":    {"/api/v1", "persistentvolumeclaims", true},
	"events":                    {"/api/v1", "events", true},
	"namespaces":                {"/api/v1", "namespaces", false},
	"nodes":                     {"/api/v1", "nodes", false},
	"persistentvolumes":         {"/api/v1", "persistentvolumes", false},
	"deployments":               {"/apis/apps/v1", "deployments", true},
	"daemonsets":                {"/apis/apps/v1", "daemonsets", true},
	"statefulsets":              {"/apis/apps/v1", "statefulsets", true},
	"replicasets":               {"/apis/apps/v1", "replicasets", true},
	"jobs":                      {"/apis/batch/v1", "jobs", true},
	"cronjobs":                  {"/apis/batch/v1", "cronjobs", true},
	"ingresses":                 {"/apis/networking.k8s.io/v1", "ingresses", true},
	"ingressclasses":            {"/apis/networking.k8s.io/v1", "ingressclasses", false},
	"networkpolicies":           {"/apis/networking.k8s.io/v1", "networkpolicies", true},
	"storageclasses":            {"/apis/storage.k8s.io/v1", "storageclasses", false},
	"clusterroles":              {"/apis/rbac.authorization.k8s.io/v1", "clusterroles", false},
	"clusterrolebindings":       {"/apis/rbac.authorization.k8s.io/v1", "clusterrolebindings", false},
	"roles":                     {"/apis/rbac.authorization.k8s.io/v1", "roles", true},
	"rolebindings":              {"/apis/rbac.authorization.k8s.io/v1", "rolebindings", true},
	"customresourcedefinitions": {"/apis/apiextensions.k8s.io/v1", "customresourcedefinitions", false},
}

// resourceAliases maps short/singular forms to the canonical plural key in resourceMap.
var resourceAliases = map[string]string{
	"pod": "pods", "deployment": "deployments", "daemonset": "daemonsets",
	"statefulset": "statefulsets", "replicaset": "replicasets", "job": "jobs",
	"cronjob": "cronjobs", "service": "services", "svc": "services",
	"ingress": "ingresses", "ingressclass": "ingressclasses",
	"configmap": "configmaps", "secret": "secrets", "pvc": "persistentvolumeclaims",
	"storageclass": "storageclasses", "crd": "customresourcedefinitions",
	"crds": "customresourcedefinitions", "node": "nodes", "namespace": "namespaces",
	"pv": "persistentvolumes", "persistentvolume": "persistentvolumes",
	"clusterrole": "clusterroles", "clusterrolebinding": "clusterrolebindings",
	"role": "roles", "rolebinding": "rolebindings",
	"serviceaccount": "serviceaccounts", "sa": "serviceaccounts",
	"networkpolicy": "networkpolicies", "event": "events",
}

func resolveResource(name string) (resourceInfo, error) {
	if info, ok := resourceMap[name]; ok {
		return info, nil
	}
	if canonical, ok := resourceAliases[name]; ok {
		return resourceMap[canonical], nil
	}
	return resourceInfo{}, fmt.Errorf("unknown resource type: %s", name)
}

func (d *Datasource) runYAMLQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (*data.Frame, error) {
	var jdata jsonData
	if err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &jdata); err != nil {
		return nil, err
	}
	secrets := pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData
	kubeConfig, err := buildKubeConfig(ctx, jdata, secrets)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	if qm.Name == "" {
		return nil, fmt.Errorf("name is required for yaml action")
	}

	namespace := qm.Namespace
	if namespace == "_all" {
		namespace = ""
	}

	var path string
	info, err := resolveResource(qm.Resource)
	if err == nil {
		if info.namespaced && namespace != "" {
			path = info.apiPath + "/namespaces/" + namespace + "/" + info.resource + "/" + qm.Name
		} else {
			path = info.apiPath + "/" + info.resource + "/" + qm.Name
		}
	} else {
		// Fallback: discover as custom resource
		gvr, discErr := discoverCR(clientset.Discovery(), qm.Resource)
		if discErr != nil {
			return nil, fmt.Errorf("unknown resource type: %s (%v)", qm.Resource, discErr)
		}
		apiPath := "/apis/" + gvr.Group + "/" + gvr.Version
		if namespace != "" {
			path = apiPath + "/namespaces/" + namespace + "/" + gvr.Resource + "/" + qm.Name
		} else {
			path = apiPath + "/" + gvr.Resource + "/" + qm.Name
		}
	}

	restResult := clientset.RESTClient().Get().AbsPath(path).Do(ctx)
	if err := restResult.Error(); err != nil {
		return nil, err
	}
	rawBytes, err := restResult.Raw()
	if err != nil {
		return nil, err
	}

	// Convert JSON to YAML
	var obj interface{}
	if err := json.Unmarshal(rawBytes, &obj); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	yamlBytes, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("converting to YAML: %w", err)
	}

	frame := data.NewFrame("yaml",
		data.NewField("yaml", nil, []string{string(yamlBytes)}),
	)
	return frame, nil
}

func labelsToString(m map[string]string) string {
	if len(m) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ", ")
}

func getPodDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var totalRestarts int32
	for _, cs := range pod.Status.ContainerStatuses {
		totalRestarts += cs.RestartCount
	}
	ownerKind, ownerName := "", ""
	if len(pod.OwnerReferences) > 0 {
		ownerKind = pod.OwnerReferences[0].Kind
		ownerName = pod.OwnerReferences[0].Name
	}
	var imagePullSecretNames []string
	for _, s := range pod.Spec.ImagePullSecrets {
		imagePullSecretNames = append(imagePullSecretNames, s.Name)
	}
	ipsJSON, _ := json.Marshal(imagePullSecretNames)
	labelsJSON, _ := json.Marshal(pod.Labels)
	annotationsJSON, _ := json.Marshal(pod.Annotations)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Node", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("IP", nil, []string{}),
		data.NewField("QoS Class", nil, []string{}),
		data.NewField("Restarts", nil, []int32{}),
		data.NewField("Service Account", nil, []string{}),
		data.NewField("Image Pull Secrets", nil, []json.RawMessage{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		pod.Name, pod.Namespace, string(pod.UID), pod.CreationTimestamp.Time,
		ownerKind, ownerName, json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		pod.Spec.NodeName, string(pod.Status.Phase), pod.Status.PodIP,
		string(pod.Status.QOSClass), totalRestarts, pod.Spec.ServiceAccountName,
		json.RawMessage(ipsJSON),
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, cond := range pod.Status.Conditions {
		var lpt, ltt *time.Time
		if !cond.LastProbeTime.IsZero() {
			t := cond.LastProbeTime.Time
			lpt = &t
		}
		if !cond.LastTransitionTime.IsZero() {
			t := cond.LastTransitionTime.Time
			ltt = &t
		}
		condFrame.AppendRow(string(cond.Type), string(cond.Status), lpt, ltt, cond.Reason, cond.Message)
	}

	evtFrame := data.NewFrame("events",
		data.NewField("Name", nil, []string{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
		data.NewField("Source", nil, []string{}),
		data.NewField("Sub Object", nil, []string{}),
		data.NewField("Count", nil, []int32{}),
		data.NewField("First Seen", nil, []time.Time{}),
		data.NewField("Last Seen", nil, []time.Time{}),
	)
	setDetailMeta(evtFrame)
	events, _ := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if events != nil {
		for _, evt := range events.Items {
			source := evt.Source.Component
			if evt.Source.Host != "" {
				source += " " + evt.Source.Host
			}
			evtFrame.AppendRow(
				evt.Name, evt.Reason, evt.Message,
				source, evt.InvolvedObject.FieldPath,
				evt.Count, evt.FirstTimestamp.Time, evt.LastTimestamp.Time,
			)
		}
	}

	// Build a lookup map of volume name → volume source type and source name
	type volumeInfo struct {
		Type       string `json:"type"`
		SourceName string `json:"sourceName"`
	}
	volumeMap := make(map[string]volumeInfo)
	for _, vol := range pod.Spec.Volumes {
		vi := volumeInfo{}
		switch {
		case vol.ConfigMap != nil:
			vi.Type, vi.SourceName = "ConfigMap", vol.ConfigMap.Name
		case vol.Secret != nil:
			vi.Type, vi.SourceName = "Secret", vol.Secret.SecretName
		case vol.PersistentVolumeClaim != nil:
			vi.Type, vi.SourceName = "PVC", vol.PersistentVolumeClaim.ClaimName
		case vol.EmptyDir != nil:
			vi.Type, vi.SourceName = "EmptyDir", ""
		case vol.HostPath != nil:
			vi.Type, vi.SourceName = "HostPath", vol.HostPath.Path
		case vol.Projected != nil:
			vi.Type, vi.SourceName = "Projected", ""
		default:
			vi.Type = "Unknown"
		}
		volumeMap[vol.Name] = vi
	}

	// Build a lookup map of container name → container status
	statusMap := make(map[string]struct {
		Ready   bool
		Started bool
		Reason  string
	})
	for _, cs := range pod.Status.ContainerStatuses {
		started := false
		if cs.Started != nil {
			started = *cs.Started
		}
		reason := ""
		if cs.State.Waiting != nil {
			reason = cs.State.Waiting.Reason
		} else if cs.State.Terminated != nil {
			reason = cs.State.Terminated.Reason
		}
		statusMap[cs.Name] = struct {
			Ready   bool
			Started bool
			Reason  string
		}{cs.Ready, started, reason}
	}

	containersFrame := data.NewFrame("containers",
		data.NewField("Name", nil, []string{}),
		data.NewField("Image", nil, []string{}),
		data.NewField("Ready", nil, []bool{}),
		data.NewField("Started", nil, []bool{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Env", nil, []json.RawMessage{}),
		data.NewField("Command", nil, []json.RawMessage{}),
		data.NewField("Args", nil, []json.RawMessage{}),
		data.NewField("Mounts", nil, []json.RawMessage{}),
		data.NewField("Readiness Probe", nil, []json.RawMessage{}),
		data.NewField("Security Context", nil, []json.RawMessage{}),
		data.NewField("Limits", nil, []json.RawMessage{}),
		data.NewField("Requests", nil, []json.RawMessage{}),
	)
	setDetailMeta(containersFrame)

	for _, c := range pod.Spec.Containers {
		st := statusMap[c.Name]

		// env
		envJSON := buildContainerEnvJSON(ctx, clientset, namespace, c)

		// mounts
		type mountEntry struct {
			Name       string `json:"name"`
			ReadOnly   bool   `json:"readOnly"`
			MountPath  string `json:"mountPath"`
			Type       string `json:"type"`
			SourceName string `json:"sourceName"`
		}
		mounts := make([]mountEntry, 0, len(c.VolumeMounts))
		for _, vm := range c.VolumeMounts {
			vi := volumeMap[vm.Name]
			mounts = append(mounts, mountEntry{
				Name:       vm.Name,
				ReadOnly:   vm.ReadOnly,
				MountPath:  vm.MountPath,
				Type:       vi.Type,
				SourceName: vi.SourceName,
			})
		}
		mountsJSON, _ := json.Marshal(mounts)

		// probes and security context
		probeJSON, _ := json.Marshal(c.ReadinessProbe)
		scJSON, _ := json.Marshal(c.SecurityContext)

		// resource limits/requests
		limits := make(map[string]string)
		for k, v := range c.Resources.Limits {
			limits[string(k)] = v.String()
		}
		requests := make(map[string]string)
		for k, v := range c.Resources.Requests {
			requests[string(k)] = v.String()
		}
		limitsJSON, _ := json.Marshal(limits)
		requestsJSON, _ := json.Marshal(requests)

		cmdJSON, _ := json.Marshal(c.Command)
		argsJSON, _ := json.Marshal(c.Args)

		containersFrame.AppendRow(
			c.Name, c.Image, st.Ready, st.Started, st.Reason,
			json.RawMessage(envJSON), json.RawMessage(cmdJSON), json.RawMessage(argsJSON),
			json.RawMessage(mountsJSON), json.RawMessage(probeJSON),
			json.RawMessage(scJSON), json.RawMessage(limitsJSON), json.RawMessage(requestsJSON),
		)
	}

	return data.Frames{metaFrame, condFrame, evtFrame, containersFrame}, nil
}

func getDeploymentDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(dep.OwnerReferences) > 0 {
		ownerKind = dep.OwnerReferences[0].Kind
		ownerName = dep.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(dep.Labels)
	annotationsJSON, _ := json.Marshal(dep.Annotations)

	revHistLimit := int32(0)
	if dep.Spec.RevisionHistoryLimit != nil {
		revHistLimit = *dep.Spec.RevisionHistoryLimit
	}
	maxSurge, maxUnavailable := "", ""
	if dep.Spec.Strategy.RollingUpdate != nil {
		if dep.Spec.Strategy.RollingUpdate.MaxSurge != nil {
			maxSurge = dep.Spec.Strategy.RollingUpdate.MaxSurge.String()
		}
		if dep.Spec.Strategy.RollingUpdate.MaxUnavailable != nil {
			maxUnavailable = dep.Spec.Strategy.RollingUpdate.MaxUnavailable.String()
		}
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Strategy", nil, []string{}),
		data.NewField("Replicas", nil, []int32{}),
		data.NewField("Ready", nil, []int32{}),
		data.NewField("Available", nil, []int32{}),
		data.NewField("Updated", nil, []int32{}),
		data.NewField("Min Ready Seconds", nil, []int32{}),
		data.NewField("Revision History Limit", nil, []int32{}),
		data.NewField("Selector", nil, []string{}),
		data.NewField("Max Surge", nil, []string{}),
		data.NewField("Max Unavailable", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		dep.Name, dep.Namespace, string(dep.UID), dep.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		string(dep.Spec.Strategy.Type),
		dep.Status.Replicas, dep.Status.ReadyReplicas, dep.Status.AvailableReplicas, dep.Status.UpdatedReplicas,
		dep.Spec.MinReadySeconds, revHistLimit,
		labelsToString(dep.Spec.Selector.MatchLabels), maxSurge, maxUnavailable,
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, cond := range dep.Status.Conditions {
		var lut *time.Time
		if !cond.LastUpdateTime.IsZero() {
			t := cond.LastUpdateTime.Time
			lut = &t
		}
		var ltt *time.Time
		if !cond.LastTransitionTime.IsZero() {
			t := cond.LastTransitionTime.Time
			ltt = &t
		}
		condFrame.AppendRow(string(cond.Type), string(cond.Status), lut, ltt, cond.Reason, cond.Message)
	}

	evtFrame := data.NewFrame("events",
		data.NewField("Name", nil, []string{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
		data.NewField("Source", nil, []string{}),
		data.NewField("Sub Object", nil, []string{}),
		data.NewField("Count", nil, []int32{}),
		data.NewField("First Seen", nil, []time.Time{}),
		data.NewField("Last Seen", nil, []time.Time{}),
	)
	setDetailMeta(evtFrame)
	events, _ := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if events != nil {
		for _, evt := range events.Items {
			source := evt.Source.Component
			if evt.Source.Host != "" {
				source += " " + evt.Source.Host
			}
			evtFrame.AppendRow(
				evt.Name, evt.Reason, evt.Message,
				source, evt.InvolvedObject.FieldPath,
				evt.Count, evt.FirstTimestamp.Time, evt.LastTimestamp.Time,
			)
		}
	}

	containersFrame := data.NewFrame("containers",
		data.NewField("Name", nil, []string{}),
		data.NewField("Image", nil, []string{}),
		data.NewField("Env", nil, []json.RawMessage{}),
		data.NewField("Command", nil, []json.RawMessage{}),
		data.NewField("Args", nil, []json.RawMessage{}),
		data.NewField("Limits", nil, []json.RawMessage{}),
		data.NewField("Requests", nil, []json.RawMessage{}),
	)
	setDetailMeta(containersFrame)
	for _, c := range dep.Spec.Template.Spec.Containers {
		envJSON := buildContainerEnvJSON(ctx, clientset, namespace, c)
		limits := make(map[string]string)
		for k, v := range c.Resources.Limits {
			limits[string(k)] = v.String()
		}
		requests := make(map[string]string)
		for k, v := range c.Resources.Requests {
			requests[string(k)] = v.String()
		}
		limitsJSON, _ := json.Marshal(limits)
		requestsJSON, _ := json.Marshal(requests)
		cmdJSON, _ := json.Marshal(c.Command)
		argsJSON, _ := json.Marshal(c.Args)
		containersFrame.AppendRow(
			c.Name, c.Image,
			json.RawMessage(envJSON), json.RawMessage(cmdJSON), json.RawMessage(argsJSON),
			json.RawMessage(limitsJSON), json.RawMessage(requestsJSON),
		)
	}

	// ReplicaSets owned by this deployment
	var selectorParts []string
	for k, v := range dep.Spec.Selector.MatchLabels {
		selectorParts = append(selectorParts, k+"="+v)
	}
	currentRevision := dep.Annotations["deployment.kubernetes.io/revision"]
	rsList, _ := clientset.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.Join(selectorParts, ","),
	})
	rsFrame := data.NewFrame("replicasets",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Is New", nil, []bool{}),
		data.NewField("Replicas", nil, []int32{}),
		data.NewField("Ready", nil, []int32{}),
		data.NewField("Available", nil, []int32{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Images", nil, []json.RawMessage{}),
	)
	setDetailMeta(rsFrame)
	if rsList != nil {
		for _, rs := range rsList.Items {
			isNew := currentRevision != "" && rs.Annotations["deployment.kubernetes.io/revision"] == currentRevision
			rsLabelsJSON, _ := json.Marshal(rs.Labels)
			images := make([]string, 0, len(rs.Spec.Template.Spec.Containers))
			for _, c := range rs.Spec.Template.Spec.Containers {
				images = append(images, c.Image)
			}
			imagesJSON, _ := json.Marshal(images)
			rsFrame.AppendRow(
				rs.Name, rs.Namespace, rs.CreationTimestamp.Time,
				isNew,
				rs.Status.Replicas, rs.Status.ReadyReplicas, rs.Status.AvailableReplicas,
				json.RawMessage(rsLabelsJSON), json.RawMessage(imagesJSON),
			)
		}
	}

	// HPAs targeting this deployment
	hpaList, _ := clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{})
	hpaFrame := data.NewFrame("hpas",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Min Pods", nil, []int32{}),
		data.NewField("Max Pods", nil, []int32{}),
		data.NewField("Current Replicas", nil, []int32{}),
		data.NewField("Desired Replicas", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)
	setDetailMeta(hpaFrame)
	if hpaList != nil {
		for _, hpa := range hpaList.Items {
			if hpa.Spec.ScaleTargetRef.Kind == "Deployment" && hpa.Spec.ScaleTargetRef.Name == name {
				minReplicas := int32(1)
				if hpa.Spec.MinReplicas != nil {
					minReplicas = *hpa.Spec.MinReplicas
				}
				hpaFrame.AppendRow(
					hpa.Name, hpa.Namespace,
					minReplicas, hpa.Spec.MaxReplicas,
					hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas,
					hpa.CreationTimestamp.Time,
				)
			}
		}
	}

	podsFrame, _ := buildPodsSubFrame(ctx, clientset, namespace, dep.Spec.Selector.MatchLabels)
	svcsFrame, _ := buildServicesSubFrame(ctx, clientset, namespace, dep.Spec.Template.Labels)

	return data.Frames{metaFrame, condFrame, evtFrame, containersFrame, rsFrame, hpaFrame, podsFrame, svcsFrame}, nil
}

func getDaemonSetDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	ds, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(ds.OwnerReferences) > 0 {
		ownerKind = ds.OwnerReferences[0].Kind
		ownerName = ds.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(ds.Labels)
	annotationsJSON, _ := json.Marshal(ds.Annotations)
	selectorJSON, _ := json.Marshal(ds.Spec.Selector.MatchLabels)
	images := make([]string, 0, len(ds.Spec.Template.Spec.Containers))
	for _, c := range ds.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}
	imagesJSON, _ := json.Marshal(images)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Selector", nil, []json.RawMessage{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Number Running", nil, []int32{}),
		data.NewField("Number Desired", nil, []int32{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		ds.Name, ds.Namespace, string(ds.UID), ds.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"DaemonSet",
		json.RawMessage(selectorJSON), json.RawMessage(imagesJSON),
		ds.Status.NumberReady, ds.Status.DesiredNumberScheduled,
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, cond := range ds.Status.Conditions {
		var ltt *time.Time
		if !cond.LastTransitionTime.IsZero() {
			t := cond.LastTransitionTime.Time
			ltt = &t
		}
		condFrame.AppendRow(string(cond.Type), string(cond.Status), (*time.Time)(nil), ltt, cond.Reason, cond.Message)
	}

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)
	containersFrame := buildSpecContainersFrame(ctx, clientset, namespace, ds.Spec.Template.Spec.Containers)
	podsFrame, _ := buildPodsSubFrame(ctx, clientset, namespace, ds.Spec.Selector.MatchLabels)
	svcsFrame, _ := buildServicesSubFrame(ctx, clientset, namespace, ds.Spec.Template.Labels)

	return data.Frames{metaFrame, condFrame, evtFrame, containersFrame, podsFrame, svcsFrame}, nil
}

func getStatefulSetDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	ss, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(ss.OwnerReferences) > 0 {
		ownerKind = ss.OwnerReferences[0].Kind
		ownerName = ss.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(ss.Labels)
	annotationsJSON, _ := json.Marshal(ss.Annotations)
	images := make([]string, 0, len(ss.Spec.Template.Spec.Containers))
	for _, c := range ss.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}
	imagesJSON, _ := json.Marshal(images)
	desired := int32(1)
	if ss.Spec.Replicas != nil {
		desired = *ss.Spec.Replicas
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Number Running", nil, []int32{}),
		data.NewField("Number Desired", nil, []int32{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		ss.Name, ss.Namespace, string(ss.UID), ss.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"StatefulSet",
		json.RawMessage(imagesJSON),
		ss.Status.ReadyReplicas, desired,
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, cond := range ss.Status.Conditions {
		var ltt *time.Time
		if !cond.LastTransitionTime.IsZero() {
			t := cond.LastTransitionTime.Time
			ltt = &t
		}
		condFrame.AppendRow(string(cond.Type), string(cond.Status), (*time.Time)(nil), ltt, cond.Reason, cond.Message)
	}

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)
	containersFrame := buildSpecContainersFrame(ctx, clientset, namespace, ss.Spec.Template.Spec.Containers)
	podsFrame, _ := buildPodsSubFrame(ctx, clientset, namespace, ss.Spec.Selector.MatchLabels)

	return data.Frames{metaFrame, condFrame, evtFrame, containersFrame, podsFrame}, nil
}

func getReplicaSetDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	rs, err := clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(rs.OwnerReferences) > 0 {
		ownerKind = rs.OwnerReferences[0].Kind
		ownerName = rs.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(rs.Labels)
	annotationsJSON, _ := json.Marshal(rs.Annotations)
	selectorJSON, _ := json.Marshal(rs.Spec.Selector.MatchLabels)
	images := make([]string, 0, len(rs.Spec.Template.Spec.Containers))
	for _, c := range rs.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}
	imagesJSON, _ := json.Marshal(images)
	desired := int32(1)
	if rs.Spec.Replicas != nil {
		desired = *rs.Spec.Replicas
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Selector", nil, []json.RawMessage{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Number Running", nil, []int32{}),
		data.NewField("Number Desired", nil, []int32{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		rs.Name, rs.Namespace, string(rs.UID), rs.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"ReplicaSet",
		json.RawMessage(selectorJSON), json.RawMessage(imagesJSON),
		rs.Status.ReadyReplicas, desired,
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, cond := range rs.Status.Conditions {
		var ltt *time.Time
		if !cond.LastTransitionTime.IsZero() {
			t := cond.LastTransitionTime.Time
			ltt = &t
		}
		condFrame.AppendRow(string(cond.Type), string(cond.Status), (*time.Time)(nil), ltt, cond.Reason, cond.Message)
	}

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)
	containersFrame := buildSpecContainersFrame(ctx, clientset, namespace, rs.Spec.Template.Spec.Containers)
	podsFrame, _ := buildPodsSubFrame(ctx, clientset, namespace, rs.Spec.Selector.MatchLabels)
	svcsFrame, _ := buildServicesSubFrame(ctx, clientset, namespace, rs.Spec.Template.Labels)

	return data.Frames{metaFrame, condFrame, evtFrame, containersFrame, podsFrame, svcsFrame}, nil
}

func getJobDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(job.OwnerReferences) > 0 {
		ownerKind = job.OwnerReferences[0].Kind
		ownerName = job.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(job.Labels)
	annotationsJSON, _ := json.Marshal(job.Annotations)
	images := make([]string, 0, len(job.Spec.Template.Spec.Containers))
	for _, c := range job.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}
	imagesJSON, _ := json.Marshal(images)
	completions := int32(1)
	if job.Spec.Completions != nil {
		completions = *job.Spec.Completions
	}
	parallelism := int32(1)
	if job.Spec.Parallelism != nil {
		parallelism = *job.Spec.Parallelism
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Completions", nil, []int32{}),
		data.NewField("Parallelism", nil, []int32{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Succeeded", nil, []int32{}),
		data.NewField("Active", nil, []int32{}),
		data.NewField("Failed", nil, []int32{}),
		data.NewField("Number Desired", nil, []int32{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		job.Name, job.Namespace, string(job.UID), job.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"Job",
		completions, parallelism, json.RawMessage(imagesJSON),
		job.Status.Succeeded, job.Status.Active, job.Status.Failed,
		completions,
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, cond := range job.Status.Conditions {
		var lpt, ltt *time.Time
		if !cond.LastProbeTime.IsZero() {
			t := cond.LastProbeTime.Time
			lpt = &t
		}
		if !cond.LastTransitionTime.IsZero() {
			t := cond.LastTransitionTime.Time
			ltt = &t
		}
		condFrame.AppendRow(string(cond.Type), string(cond.Status), lpt, ltt, cond.Reason, cond.Message)
	}

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)
	containersFrame := buildSpecContainersFrame(ctx, clientset, namespace, job.Spec.Template.Spec.Containers)
	var podMatchLabels map[string]string
	if job.Spec.Selector != nil {
		podMatchLabels = job.Spec.Selector.MatchLabels
	} else {
		podMatchLabels = job.Spec.Template.Labels
	}
	podsFrame, _ := buildPodsSubFrame(ctx, clientset, namespace, podMatchLabels)

	return data.Frames{metaFrame, condFrame, evtFrame, containersFrame, podsFrame}, nil
}

func getCronJobDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	cj, err := clientset.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(cj.OwnerReferences) > 0 {
		ownerKind = cj.OwnerReferences[0].Kind
		ownerName = cj.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(cj.Labels)
	annotationsJSON, _ := json.Marshal(cj.Annotations)
	suspend := false
	if cj.Spec.Suspend != nil {
		suspend = *cj.Spec.Suspend
	}
	activeCount := int32(len(cj.Status.Active))
	var lastSchedule *time.Time
	if cj.Status.LastScheduleTime != nil {
		t := cj.Status.LastScheduleTime.Time
		lastSchedule = &t
	}
	startingDeadline := int64(0)
	if cj.Spec.StartingDeadlineSeconds != nil {
		startingDeadline = *cj.Spec.StartingDeadlineSeconds
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Schedule", nil, []string{}),
		data.NewField("Active Jobs", nil, []int32{}),
		data.NewField("Suspend", nil, []bool{}),
		data.NewField("Last Schedule", nil, []*time.Time{}),
		data.NewField("Concurrency Policy", nil, []string{}),
		data.NewField("Starting Deadline Seconds", nil, []int64{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		cj.Name, cj.Namespace, string(cj.UID), cj.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"CronJob",
		cj.Spec.Schedule, activeCount, suspend, lastSchedule,
		string(cj.Spec.ConcurrencyPolicy), startingDeadline,
	)

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)

	buildJobSubFrame := func(frameName string) *data.Frame {
		f := data.NewFrame(frameName,
			data.NewField("Name", nil, []string{}),
			data.NewField("Namespace", nil, []string{}),
			data.NewField("Images", nil, []json.RawMessage{}),
			data.NewField("Labels", nil, []json.RawMessage{}),
			data.NewField("Pods", nil, []string{}),
			data.NewField("Created", nil, []time.Time{}),
		)
		setDetailMeta(f)
		return f
	}

	activeJobsFrame := buildJobSubFrame("active_jobs")
	for _, ref := range cj.Status.Active {
		activeJob, err := clientset.BatchV1().Jobs(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		images := make([]string, 0, len(activeJob.Spec.Template.Spec.Containers))
		for _, c := range activeJob.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}
		imagesJSON, _ := json.Marshal(images)
		jLabelsJSON, _ := json.Marshal(activeJob.Labels)
		completions := int32(1)
		if activeJob.Spec.Completions != nil {
			completions = *activeJob.Spec.Completions
		}
		pods := fmt.Sprintf("%d/%d", activeJob.Status.Succeeded, completions)
		activeJobsFrame.AppendRow(
			activeJob.Name, activeJob.Namespace,
			json.RawMessage(imagesJSON), json.RawMessage(jLabelsJSON),
			pods, activeJob.CreationTimestamp.Time,
		)
	}

	inactiveJobsFrame := buildJobSubFrame("inactive_jobs")
	allJobs, _ := clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	activeNames := make(map[string]bool)
	for _, ref := range cj.Status.Active {
		activeNames[ref.Name] = true
	}
	if allJobs != nil {
		for _, j := range allJobs.Items {
			if activeNames[j.Name] {
				continue
			}
			owned := false
			for _, owner := range j.OwnerReferences {
				if owner.Kind == "CronJob" && owner.Name == name {
					owned = true
					break
				}
			}
			if !owned {
				continue
			}
			images := make([]string, 0, len(j.Spec.Template.Spec.Containers))
			for _, c := range j.Spec.Template.Spec.Containers {
				images = append(images, c.Image)
			}
			imagesJSON, _ := json.Marshal(images)
			jLabelsJSON, _ := json.Marshal(j.Labels)
			completions := int32(1)
			if j.Spec.Completions != nil {
				completions = *j.Spec.Completions
			}
			pods := fmt.Sprintf("%d/%d", j.Status.Succeeded, completions)
			inactiveJobsFrame.AppendRow(
				j.Name, j.Namespace,
				json.RawMessage(imagesJSON), json.RawMessage(jLabelsJSON),
				pods, j.CreationTimestamp.Time,
			)
		}
	}

	return data.Frames{metaFrame, evtFrame, activeJobsFrame, inactiveJobsFrame}, nil
}

func buildEventsFrame(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) *data.Frame {
	f := data.NewFrame("events",
		data.NewField("Name", nil, []string{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
		data.NewField("Source", nil, []string{}),
		data.NewField("Sub Object", nil, []string{}),
		data.NewField("Count", nil, []int32{}),
		data.NewField("First Seen", nil, []time.Time{}),
		data.NewField("Last Seen", nil, []time.Time{}),
	)
	setDetailMeta(f)
	events, _ := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if events != nil {
		for _, evt := range events.Items {
			source := evt.Source.Component
			if evt.Source.Host != "" {
				source += " " + evt.Source.Host
			}
			f.AppendRow(
				evt.Name, evt.Reason, evt.Message,
				source, evt.InvolvedObject.FieldPath,
				evt.Count, evt.FirstTimestamp.Time, evt.LastTimestamp.Time,
			)
		}
	}
	return f
}

type envEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func buildContainerEnvJSON(ctx context.Context, clientset *kubernetes.Clientset, namespace string, c corev1.Container) json.RawMessage {
	entries := make([]envEntry, 0, len(c.Env))
	for _, e := range c.Env {
		val := e.Value
		if e.ValueFrom != nil {
			switch {
			case e.ValueFrom.ConfigMapKeyRef != nil:
				val = "configmap:" + e.ValueFrom.ConfigMapKeyRef.Name + "/" + e.ValueFrom.ConfigMapKeyRef.Key
			case e.ValueFrom.SecretKeyRef != nil:
				val = "secret:" + e.ValueFrom.SecretKeyRef.Name + "/" + e.ValueFrom.SecretKeyRef.Key
			case e.ValueFrom.FieldRef != nil:
				val = "fieldRef:" + e.ValueFrom.FieldRef.FieldPath
			case e.ValueFrom.ResourceFieldRef != nil:
				val = "resourceFieldRef:" + e.ValueFrom.ResourceFieldRef.Resource
			}
		}
		entries = append(entries, envEntry{Name: e.Name, Value: val})
	}
	for _, ef := range c.EnvFrom {
		prefix := ef.Prefix
		if ef.ConfigMapRef != nil {
			cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, ef.ConfigMapRef.Name, metav1.GetOptions{})
			if err == nil {
				for k, v := range cm.Data {
					entries = append(entries, envEntry{Name: prefix + k, Value: v})
				}
			}
		} else if ef.SecretRef != nil {
			secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, ef.SecretRef.Name, metav1.GetOptions{})
			if err == nil {
				for k := range secret.Data {
					entries = append(entries, envEntry{Name: prefix + k, Value: "***"})
				}
			}
		}
	}
	b, _ := json.Marshal(entries)
	return json.RawMessage(b)
}

func buildSpecContainersFrame(ctx context.Context, clientset *kubernetes.Clientset, namespace string, containers []corev1.Container) *data.Frame {
	f := data.NewFrame("containers",
		data.NewField("Name", nil, []string{}),
		data.NewField("Image", nil, []string{}),
		data.NewField("Env", nil, []json.RawMessage{}),
		data.NewField("Command", nil, []json.RawMessage{}),
		data.NewField("Args", nil, []json.RawMessage{}),
		data.NewField("Limits", nil, []json.RawMessage{}),
		data.NewField("Requests", nil, []json.RawMessage{}),
	)
	setDetailMeta(f)
	for _, c := range containers {
		envJSON := buildContainerEnvJSON(ctx, clientset, namespace, c)
		limits := make(map[string]string)
		for k, v := range c.Resources.Limits {
			limits[string(k)] = v.String()
		}
		requests := make(map[string]string)
		for k, v := range c.Resources.Requests {
			requests[string(k)] = v.String()
		}
		limitsJSON, _ := json.Marshal(limits)
		requestsJSON, _ := json.Marshal(requests)
		cmdJSON, _ := json.Marshal(c.Command)
		argsJSON, _ := json.Marshal(c.Args)
		f.AppendRow(
			c.Name, c.Image,
			json.RawMessage(envJSON), json.RawMessage(cmdJSON), json.RawMessage(argsJSON),
			json.RawMessage(limitsJSON), json.RawMessage(requestsJSON),
		)
	}
	return f
}

func buildPodsSubFrame(ctx context.Context, clientset *kubernetes.Clientset, namespace string, matchLabels map[string]string) (*data.Frame, error) {
	parts := make([]string, 0, len(matchLabels))
	for k, v := range matchLabels {
		parts = append(parts, k+"="+v)
	}
	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.Join(parts, ","),
	})
	if err != nil {
		return nil, err
	}
	f := data.NewFrame("pods",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Node", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Restarts", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)
	setDetailMeta(f)
	for _, pod := range podList.Items {
		images := make([]string, len(pod.Spec.Containers))
		for i, c := range pod.Spec.Containers {
			images[i] = c.Image
		}
		imagesJSON, _ := json.Marshal(images)
		podLabelsJSON, _ := json.Marshal(pod.Labels)
		var totalRestarts int32
		for _, cs := range pod.Status.ContainerStatuses {
			totalRestarts += cs.RestartCount
		}
		f.AppendRow(
			pod.Name, pod.Namespace,
			json.RawMessage(imagesJSON), json.RawMessage(podLabelsJSON),
			pod.Spec.NodeName, string(pod.Status.Phase),
			totalRestarts, pod.CreationTimestamp.Time,
		)
	}
	return f, nil
}

func buildServicesSubFrame(ctx context.Context, clientset *kubernetes.Clientset, namespace string, podTemplateLabels map[string]string) (*data.Frame, error) {
	svcList, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	f := data.NewFrame("services",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Type", nil, []string{}),
		data.NewField("Cluster IP", nil, []string{}),
		data.NewField("Ports", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)
	setDetailMeta(f)
	for _, svc := range svcList.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		matches := true
		for k, v := range svc.Spec.Selector {
			if podTemplateLabels[k] != v {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		type portInfo struct {
			Name     string `json:"name"`
			Port     int32  `json:"port"`
			Protocol string `json:"protocol"`
			NodePort int32  `json:"nodePort,omitempty"`
		}
		ports := make([]portInfo, len(svc.Spec.Ports))
		for i, p := range svc.Spec.Ports {
			ports[i] = portInfo{Name: p.Name, Port: p.Port, Protocol: string(p.Protocol), NodePort: p.NodePort}
		}
		portsJSON, _ := json.Marshal(ports)
		f.AppendRow(
			svc.Name, svc.Namespace,
			string(svc.Spec.Type), svc.Spec.ClusterIP,
			json.RawMessage(portsJSON), svc.CreationTimestamp.Time,
		)
	}
	return f, nil
}

func getServiceDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(svc.OwnerReferences) > 0 {
		ownerKind = svc.OwnerReferences[0].Kind
		ownerName = svc.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(svc.Labels)
	annotationsJSON, _ := json.Marshal(svc.Annotations)
	selectorJSON, _ := json.Marshal(svc.Spec.Selector)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Type", nil, []string{}),
		data.NewField("Cluster IP", nil, []string{}),
		data.NewField("Session Affinity", nil, []string{}),
		data.NewField("Selector", nil, []json.RawMessage{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		svc.Name, svc.Namespace, string(svc.UID), svc.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"Service",
		string(svc.Spec.Type), svc.Spec.ClusterIP, string(svc.Spec.SessionAffinity),
		json.RawMessage(selectorJSON),
	)

	endpointsFrame := data.NewFrame("endpoints",
		data.NewField("Host", nil, []string{}),
		data.NewField("Ports", nil, []string{}),
		data.NewField("Node", nil, []string{}),
		data.NewField("Ready", nil, []bool{}),
	)
	setDetailMeta(endpointsFrame)
	eps, _ := clientset.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
	if eps != nil {
		for _, subset := range eps.Subsets {
			portParts := make([]string, 0, len(subset.Ports))
			for _, p := range subset.Ports {
				portStr := ""
				if p.Name != "" {
					portStr = p.Name + ":"
				}
				portStr += fmt.Sprintf("%d,%s", p.Port, string(p.Protocol))
				portParts = append(portParts, portStr)
			}
			ports := strings.Join(portParts, "/")
			for _, addr := range subset.Addresses {
				node := ""
				if addr.NodeName != nil {
					node = *addr.NodeName
				}
				endpointsFrame.AppendRow(addr.IP, ports, node, true)
			}
			for _, addr := range subset.NotReadyAddresses {
				node := ""
				if addr.NodeName != nil {
					node = *addr.NodeName
				}
				endpointsFrame.AppendRow(addr.IP, ports, node, false)
			}
		}
	}

	podsFrame, _ := buildPodsSubFrame(ctx, clientset, namespace, svc.Spec.Selector)

	ingressesFrame := data.NewFrame("ingresses",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Endpoints", nil, []json.RawMessage{}),
		data.NewField("Hosts", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)
	setDetailMeta(ingressesFrame)
	ingressList, _ := clientset.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if ingressList != nil {
		for _, ing := range ingressList.Items {
			references := false
			for _, rule := range ing.Spec.Rules {
				if rule.HTTP == nil {
					continue
				}
				for _, path := range rule.HTTP.Paths {
					if path.Backend.Service != nil && path.Backend.Service.Name == name {
						references = true
						break
					}
				}
				if references {
					break
				}
			}
			if !references {
				continue
			}
			ingLabelsJSON, _ := json.Marshal(ing.Labels)
			lbEndpoints := make([]string, 0)
			for _, lb := range ing.Status.LoadBalancer.Ingress {
				if lb.Hostname != "" {
					lbEndpoints = append(lbEndpoints, lb.Hostname)
				} else if lb.IP != "" {
					lbEndpoints = append(lbEndpoints, lb.IP)
				}
			}
			endpointsJSON, _ := json.Marshal(lbEndpoints)
			hosts := make([]string, 0)
			for _, rule := range ing.Spec.Rules {
				if rule.Host != "" {
					hosts = append(hosts, rule.Host)
				}
			}
			hostsJSON, _ := json.Marshal(hosts)
			ingressesFrame.AppendRow(
				ing.Name, ing.Namespace,
				json.RawMessage(ingLabelsJSON),
				json.RawMessage(endpointsJSON),
				json.RawMessage(hostsJSON),
				ing.CreationTimestamp.Time,
			)
		}
	}

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)

	return data.Frames{metaFrame, endpointsFrame, podsFrame, ingressesFrame, evtFrame}, nil
}

func getIngressDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	ing, err := clientset.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ownerKind, ownerName := "", ""
	if len(ing.OwnerReferences) > 0 {
		ownerKind = ing.OwnerReferences[0].Kind
		ownerName = ing.OwnerReferences[0].Name
	}
	labelsJSON, _ := json.Marshal(ing.Labels)
	annotationsJSON, _ := json.Marshal(ing.Annotations)
	ingressClassName := ""
	if ing.Spec.IngressClassName != nil {
		ingressClassName = *ing.Spec.IngressClassName
	}
	lbEndpoints := make([]string, 0)
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.Hostname != "" {
			lbEndpoints = append(lbEndpoints, lb.Hostname)
		} else if lb.IP != "" {
			lbEndpoints = append(lbEndpoints, lb.IP)
		}
	}
	endpointsJSON, _ := json.Marshal(lbEndpoints)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Ingress Class Name", nil, []string{}),
		data.NewField("Endpoints", nil, []json.RawMessage{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		ing.Name, ing.Namespace, string(ing.UID), ing.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"Ingress",
		ingressClassName, json.RawMessage(endpointsJSON),
	)

	tlsMap := make(map[string]string)
	for _, tls := range ing.Spec.TLS {
		for _, host := range tls.Hosts {
			tlsMap[host] = tls.SecretName
		}
	}

	rulesFrame := data.NewFrame("rules",
		data.NewField("Host", nil, []string{}),
		data.NewField("Path", nil, []string{}),
		data.NewField("Path Type", nil, []string{}),
		data.NewField("Service Name", nil, []string{}),
		data.NewField("Service Port", nil, []int32{}),
		data.NewField("TLS Secret", nil, []string{}),
	)
	setDetailMeta(rulesFrame)
	for _, rule := range ing.Spec.Rules {
		tlsSecret := tlsMap[rule.Host]
		if rule.HTTP == nil {
			rulesFrame.AppendRow(rule.Host, "", "", "", int32(0), tlsSecret)
			continue
		}
		for _, path := range rule.HTTP.Paths {
			pathType := ""
			if path.PathType != nil {
				pathType = string(*path.PathType)
			}
			svcName := ""
			svcPort := int32(0)
			if path.Backend.Service != nil {
				svcName = path.Backend.Service.Name
				svcPort = path.Backend.Service.Port.Number
			}
			rulesFrame.AppendRow(rule.Host, path.Path, pathType, svcName, svcPort, tlsSecret)
		}
	}

	evtFrame := buildEventsFrame(ctx, clientset, namespace, name)

	return data.Frames{metaFrame, rulesFrame, evtFrame}, nil
}

func getIngressClassDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	ic, err := clientset.NetworkingV1().IngressClasses().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelsJSON, _ := json.Marshal(ic.Labels)
	annotationsJSON, _ := json.Marshal(ic.Annotations)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Controller", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		ic.Name, "", string(ic.UID), ic.CreationTimestamp.Time,
		"", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"IngressClass",
		ic.Spec.Controller,
	)

	return data.Frames{metaFrame}, nil
}

func getDeployments(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.AppsV1().Deployments(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Target", nil, []int32{}),
		data.NewField("Available", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pod := range list.Items {
		images := make([]string, len(pod.Spec.Template.Spec.Containers))
		for i, container := range pod.Spec.Template.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(pod.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pod.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getDaemonsets retrieves daemonsets
func getDeamonSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.AppsV1().DaemonSets(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Target", nil, []int32{}),
		data.NewField("Available", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pod := range list.Items {
		images := make([]string, len(pod.Spec.Template.Spec.Containers))
		for i, container := range pod.Spec.Template.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(pod.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pod.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), pod.Status.DesiredNumberScheduled, pod.Status.CurrentNumberScheduled, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getStatefulsets retrieves statefulsets
func getStatefulSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.AppsV1().StatefulSets(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Target", nil, []int32{}),
		data.NewField("Available", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pod := range list.Items {
		images := make([]string, len(pod.Spec.Template.Spec.Containers))
		for i, container := range pod.Spec.Template.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(pod.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pod.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getReplicasets retrieves replicasets
func getReplicaSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.AppsV1().ReplicaSets(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Target", nil, []int32{}),
		data.NewField("Available", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pod := range list.Items {
		images := make([]string, len(pod.Spec.Template.Spec.Containers))
		for i, container := range pod.Spec.Template.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(pod.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pod.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getJobs retrieves kubernetes jobs
func getJobs(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.BatchV1().Jobs(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Succeeded", nil, []int32{}),
		data.NewField("Completed", nil, []*int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, itm := range list.Items {
		images := make([]string, len(itm.Spec.Template.Spec.Containers))
		for i, container := range itm.Spec.Template.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(itm.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(itm.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(itm.Name, itm.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), itm.Status.Succeeded, itm.Spec.Completions, itm.CreationTimestamp.Time)
	}

	return frame, nil
}

// getNamespaces retrieves all namespaces in the cluster
func getNamespaces(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().Namespaces().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
	)

	for _, ns := range list.Items {
		frame.AppendRow(ns.Name)
	}

	return frame, nil
}

// getCronJobs retrieves kubernetes jobs
func getCronJobs(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.BatchV1().CronJobs(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Schedule", nil, []string{}),
		data.NewField("Suspend", nil, []*bool{}),
		data.NewField("Active", nil, []int32{}),
		data.NewField("Last Scheduled", nil, []*time.Time{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, itm := range list.Items {
		images := make([]string, len(itm.Spec.JobTemplate.Spec.Template.Spec.Containers))
		for i, container := range itm.Spec.JobTemplate.Spec.Template.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(itm.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(itm.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}

		// Handle LastScheduleTime (use zero time if nil)
		var lastScheduled *time.Time
		if itm.Status.LastScheduleTime != nil {
			lastScheduled = &itm.Status.LastScheduleTime.Time
		}

		frame.AppendRow(itm.Name, itm.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), itm.Spec.Schedule, itm.Spec.Suspend, int32(len(itm.Status.Active)), lastScheduled, itm.CreationTimestamp.Time)
	}

	return frame, nil
}

// getPods retrieves pods from the specified namespace (or all namespaces if namespace is empty)
func getPods(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {

	list, err := clientset.CoreV1().Pods(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}
	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Images", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Node", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Restarts", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pod := range list.Items {
		images := make([]string, len(pod.Spec.Containers))
		for i, container := range pod.Spec.Containers {
			images[i] = container.Image
		}
		jsonImages, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(pod.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pod.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		var totalRestarts int32
		for _, containerStatus := range pod.Status.ContainerStatuses {
			totalRestarts += containerStatus.RestartCount
		}
		frame.AppendRow(pod.Name, pod.Namespace, json.RawMessage(jsonImages), json.RawMessage(labels), json.RawMessage(annotations), pod.Spec.NodeName, string(pod.Status.Phase), totalRestarts, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

//func describePod(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
//
//	list, err := clientset.AppsV1().Deployments(namespace).List(ctx, listOpts)
//	if err != nil {
//		return nil, err
//	}
//
//	frame := data.NewFrame("Workloads",
//		data.NewField("Name", nil, []string{}),
//		data.NewField("Namespace", nil, []string{}),
//		data.NewField("Status", nil, []string{}),
//		data.NewField("Images", nil, []json.RawMessage{}),
//		data.NewField("Labels", nil, []json.RawMessage{}),
//		data.NewField("Target", nil, []int32{}),
//		data.NewField("Available", nil, []int32{}),
//		data.NewField("Created", nil, []time.Time{}),
//	)
//
//	for _, pod := range list.Items {
//		images := make([]string, len(pod.Spec.Template.Spec.Containers))
//		for i, container := range pod.Spec.Template.Spec.Containers {
//			images[i] = container.Image
//		}
//		jsonImages, err := json.Marshal(images)
//		if err != nil {
//			return nil, err
//		}
//		labels, err := json.Marshal(pod.ObjectMeta.Labels)
//		if err != nil {
//			return nil, err
//		}
//		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
//	}
//
//	return frame, nil
//}

func getServices(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().Services(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Type", nil, []string{}),
		data.NewField("Cluster IP", nil, []string{}),
		data.NewField("Ports", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, svc := range list.Items {
		type portInfo struct {
			Port     int32  `json:"port"`
			Protocol string `json:"protocol"`
			NodePort int32  `json:"nodePort,omitempty"`
		}
		ports := make([]portInfo, len(svc.Spec.Ports))
		for i, p := range svc.Spec.Ports {
			ports[i] = portInfo{Port: p.Port, Protocol: string(p.Protocol), NodePort: p.NodePort}
		}
		jsonPorts, err := json.Marshal(ports)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(svc.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(svc.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(svc.Name, svc.Namespace, string(svc.Spec.Type), svc.Spec.ClusterIP, json.RawMessage(jsonPorts), json.RawMessage(labels), json.RawMessage(annotations), svc.CreationTimestamp.Time)
	}

	return frame, nil
}

func getIngresses(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.NetworkingV1().Ingresses(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Class", nil, []string{}),
		data.NewField("Hosts", nil, []json.RawMessage{}),
		data.NewField("TLS", nil, []bool{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, ing := range list.Items {
		className := ""
		if ing.Spec.IngressClassName != nil {
			className = *ing.Spec.IngressClassName
		}
		hosts := make([]string, len(ing.Spec.Rules))
		for i, rule := range ing.Spec.Rules {
			hosts[i] = rule.Host
		}
		jsonHosts, err := json.Marshal(hosts)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(ing.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(ing.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(ing.Name, ing.Namespace, className, json.RawMessage(jsonHosts), len(ing.Spec.TLS) > 0, json.RawMessage(labels), json.RawMessage(annotations), ing.CreationTimestamp.Time)
	}

	return frame, nil
}

func getIngressClasses(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.NetworkingV1().IngressClasses().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Controller", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, ic := range list.Items {
		labels, err := json.Marshal(ic.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(ic.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(ic.Name, ic.Spec.Controller, json.RawMessage(labels), json.RawMessage(annotations), ic.CreationTimestamp.Time)
	}

	return frame, nil
}

func getConfigMaps(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().ConfigMaps(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Keys", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, cm := range list.Items {
		keys := make([]string, 0, len(cm.Data))
		for k := range cm.Data {
			keys = append(keys, k)
		}
		jsonKeys, err := json.Marshal(keys)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(cm.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(cm.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(cm.Name, cm.Namespace, json.RawMessage(jsonKeys), json.RawMessage(labels), json.RawMessage(annotations), cm.CreationTimestamp.Time)
	}

	return frame, nil
}

func getSecrets(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().Secrets(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Type", nil, []string{}),
		data.NewField("Keys", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, secret := range list.Items {
		keys := make([]string, 0, len(secret.Data))
		for k := range secret.Data {
			keys = append(keys, k)
		}
		jsonKeys, err := json.Marshal(keys)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(secret.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(secret.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(secret.Name, secret.Namespace, string(secret.Type), json.RawMessage(jsonKeys), json.RawMessage(labels), json.RawMessage(annotations), secret.CreationTimestamp.Time)
	}

	return frame, nil
}

func getPersistentVolumeClaims(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Storage Class", nil, []string{}),
		data.NewField("Capacity", nil, []string{}),
		data.NewField("Access Modes", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pvc := range list.Items {
		storageClass := ""
		if pvc.Spec.StorageClassName != nil {
			storageClass = *pvc.Spec.StorageClassName
		}
		capacity := ""
		if storage, ok := pvc.Status.Capacity["storage"]; ok {
			capacity = storage.String()
		}
		modes := make([]string, len(pvc.Spec.AccessModes))
		for i, m := range pvc.Spec.AccessModes {
			modes[i] = string(m)
		}
		jsonModes, err := json.Marshal(modes)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(pvc.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pvc.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(pvc.Name, pvc.Namespace, string(pvc.Status.Phase), storageClass, capacity, json.RawMessage(jsonModes), json.RawMessage(labels), json.RawMessage(annotations), pvc.CreationTimestamp.Time)
	}

	return frame, nil
}

func getStorageClasses(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.StorageV1().StorageClasses().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Provisioner", nil, []string{}),
		data.NewField("Reclaim Policy", nil, []string{}),
		data.NewField("Volume Binding Mode", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, sc := range list.Items {
		reclaimPolicy := ""
		if sc.ReclaimPolicy != nil {
			reclaimPolicy = string(*sc.ReclaimPolicy)
		}
		bindingMode := ""
		if sc.VolumeBindingMode != nil {
			bindingMode = string(*sc.VolumeBindingMode)
		}
		labels, err := json.Marshal(sc.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(sc.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(sc.Name, sc.Provisioner, reclaimPolicy, bindingMode, json.RawMessage(labels), json.RawMessage(annotations), sc.CreationTimestamp.Time)
	}

	return frame, nil
}

func getCRDs(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	restResult := clientset.RESTClient().Get().
		AbsPath("/apis/apiextensions.k8s.io/v1/customresourcedefinitions").
		Param("labelSelector", listOpts.LabelSelector).
		Do(ctx)
	if err := restResult.Error(); err != nil {
		return nil, err
	}
	rawBytes, err := restResult.Raw()
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp time.Time         `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Group string `json:"group"`
				Scope string `json:"scope"`
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rawBytes, &result); err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Group", nil, []string{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Scope", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, crd := range result.Items {
		labels, err := json.Marshal(crd.Metadata.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(crd.Metadata.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(crd.Metadata.Name, crd.Spec.Group, crd.Spec.Names.Kind, crd.Spec.Scope, json.RawMessage(labels), json.RawMessage(annotations), crd.Metadata.CreationTimestamp)
	}

	return frame, nil
}

func getClusterRoleBindings(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.RbacV1().ClusterRoleBindings().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Role", nil, []string{}),
		data.NewField("Subjects", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, crb := range list.Items {
		type subject struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace,omitempty"`
		}
		subjects := make([]subject, len(crb.Subjects))
		for i, s := range crb.Subjects {
			subjects[i] = subject{Kind: s.Kind, Name: s.Name, Namespace: s.Namespace}
		}
		jsonSubjects, err := json.Marshal(subjects)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(crb.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(crb.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		roleRef := crb.RoleRef.Kind + "/" + crb.RoleRef.Name
		frame.AppendRow(crb.Name, roleRef, json.RawMessage(jsonSubjects), json.RawMessage(labels), json.RawMessage(annotations), crb.CreationTimestamp.Time)
	}

	return frame, nil
}

func getClusterRoles(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.RbacV1().ClusterRoles().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Rules", nil, []int32{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, cr := range list.Items {
		labels, err := json.Marshal(cr.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(cr.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(cr.Name, int32(len(cr.Rules)), json.RawMessage(labels), json.RawMessage(annotations), cr.CreationTimestamp.Time)
	}

	return frame, nil
}

func getEvents(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().Events(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Type", nil, []string{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Object", nil, []string{}),
		data.NewField("Message", nil, []string{}),
		data.NewField("Count", nil, []int32{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, ev := range list.Items {
		regarding := ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name
		frame.AppendRow(ev.Name, ev.Namespace, ev.Type, ev.Reason, regarding, ev.Message, ev.Count, ev.CreationTimestamp.Time)
	}

	return frame, nil
}

func getNetworkPolicies(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Pod Selector", nil, []string{}),
		data.NewField("Policy Types", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, np := range list.Items {
		podSelector := metav1.FormatLabelSelector(&np.Spec.PodSelector)
		types := make([]string, len(np.Spec.PolicyTypes))
		for i, t := range np.Spec.PolicyTypes {
			types[i] = string(t)
		}
		jsonTypes, err := json.Marshal(types)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(np.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(np.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(np.Name, np.Namespace, podSelector, json.RawMessage(jsonTypes), json.RawMessage(labels), json.RawMessage(annotations), np.CreationTimestamp.Time)
	}

	return frame, nil
}

func getNodes(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().Nodes().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	// Fetch all pods to compute per-node resource requests/limits and pod counts
	podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	type nodeResources struct {
		cpuRequests    int64
		cpuLimits      int64
		memoryRequests int64
		memoryLimits   int64
		podCount       int
	}
	nodeResourceMap := make(map[string]*nodeResources)
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		nr, ok := nodeResourceMap[pod.Spec.NodeName]
		if !ok {
			nr = &nodeResources{}
			nodeResourceMap[pod.Spec.NodeName] = nr
		}
		nr.podCount++
		for _, c := range pod.Spec.Containers {
			if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				nr.cpuRequests += req.MilliValue()
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
				nr.cpuLimits += lim.MilliValue()
			}
			if req, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				nr.memoryRequests += req.Value()
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				nr.memoryLimits += lim.Value()
			}
		}
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Roles", nil, []string{}),
		data.NewField("Pods", nil, []string{}),
		data.NewField("CPU Requests", nil, []string{}),
		data.NewField("CPU Limits", nil, []string{}),
		data.NewField("Memory Requests", nil, []string{}),
		data.NewField("Memory Limits", nil, []string{}),
		data.NewField("Version", nil, []string{}),
		data.NewField("OS", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, node := range list.Items {
		status := "Unknown"
		for _, condition := range node.Status.Conditions {
			if string(condition.Type) == "Ready" {
				if string(condition.Status) == "True" {
					status = "Ready"
				} else {
					status = "NotReady"
				}
				break
			}
		}
		var roles []string
		for label := range node.Labels {
			if strings.HasPrefix(label, "node-role.kubernetes.io/") {
				roles = append(roles, strings.TrimPrefix(label, "node-role.kubernetes.io/"))
			}
		}
		labels, err := json.Marshal(node.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(node.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}

		nr := nodeResourceMap[node.Name]
		if nr == nil {
			nr = &nodeResources{}
		}

		allocPods := int64(110) // default
		if ap, ok := node.Status.Allocatable[corev1.ResourcePods]; ok {
			allocPods = ap.Value()
		}

		cpuCap := int64(0)
		if c, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
			cpuCap = c.MilliValue()
		}
		memCap := int64(0)
		if m, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
			memCap = m.Value()
		}

		podStr := fmt.Sprintf("%d/%d", nr.podCount, allocPods)
		cpuReqStr := fmt.Sprintf("%dm/%dm", nr.cpuRequests, cpuCap)
		cpuLimStr := fmt.Sprintf("%dm/%dm", nr.cpuLimits, cpuCap)
		memReqStr := fmt.Sprintf("%dMi/%dMi", nr.memoryRequests/(1024*1024), memCap/(1024*1024))
		memLimStr := fmt.Sprintf("%dMi/%dMi", nr.memoryLimits/(1024*1024), memCap/(1024*1024))

		frame.AppendRow(node.Name, status, strings.Join(roles, ","),
			podStr, cpuReqStr, cpuLimStr, memReqStr, memLimStr,
			node.Status.NodeInfo.KubeletVersion, node.Status.NodeInfo.OSImage,
			json.RawMessage(labels), json.RawMessage(annotations), node.CreationTimestamp.Time)
	}

	return frame, nil
}

func getPersistentVolumes(ctx context.Context, clientset *kubernetes.Clientset, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().PersistentVolumes().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Storage Class", nil, []string{}),
		data.NewField("Capacity", nil, []string{}),
		data.NewField("Access Modes", nil, []json.RawMessage{}),
		data.NewField("Reclaim Policy", nil, []string{}),
		data.NewField("Claim", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, pv := range list.Items {
		capacity := ""
		if storage, ok := pv.Spec.Capacity["storage"]; ok {
			capacity = storage.String()
		}
		modes := make([]string, len(pv.Spec.AccessModes))
		for i, m := range pv.Spec.AccessModes {
			modes[i] = string(m)
		}
		jsonModes, err := json.Marshal(modes)
		if err != nil {
			return nil, err
		}
		claim := ""
		if pv.Spec.ClaimRef != nil {
			claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
		}
		labels, err := json.Marshal(pv.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(pv.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(pv.Name, string(pv.Status.Phase), pv.Spec.StorageClassName, capacity, json.RawMessage(jsonModes), string(pv.Spec.PersistentVolumeReclaimPolicy), claim, json.RawMessage(labels), json.RawMessage(annotations), pv.CreationTimestamp.Time)
	}

	return frame, nil
}

func getRoleBindings(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.RbacV1().RoleBindings(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Role", nil, []string{}),
		data.NewField("Subjects", nil, []json.RawMessage{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, rb := range list.Items {
		type subject struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace,omitempty"`
		}
		subjects := make([]subject, len(rb.Subjects))
		for i, s := range rb.Subjects {
			subjects[i] = subject{Kind: s.Kind, Name: s.Name, Namespace: s.Namespace}
		}
		jsonSubjects, err := json.Marshal(subjects)
		if err != nil {
			return nil, err
		}
		labels, err := json.Marshal(rb.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(rb.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		roleRef := rb.RoleRef.Kind + "/" + rb.RoleRef.Name
		frame.AppendRow(rb.Name, rb.Namespace, roleRef, json.RawMessage(jsonSubjects), json.RawMessage(labels), json.RawMessage(annotations), rb.CreationTimestamp.Time)
	}

	return frame, nil
}

func getRoles(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.RbacV1().Roles(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Rules", nil, []int32{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, role := range list.Items {
		labels, err := json.Marshal(role.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(role.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(role.Name, role.Namespace, int32(len(role.Rules)), json.RawMessage(labels), json.RawMessage(annotations), role.CreationTimestamp.Time)
	}

	return frame, nil
}

func getServiceAccounts(ctx context.Context, clientset *kubernetes.Clientset, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	list, err := clientset.CoreV1().ServiceAccounts(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Secrets", nil, []int32{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, sa := range list.Items {
		labels, err := json.Marshal(sa.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		annotations, err := json.Marshal(sa.ObjectMeta.Annotations)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(sa.Name, sa.Namespace, int32(len(sa.Secrets)), json.RawMessage(labels), json.RawMessage(annotations), sa.CreationTimestamp.Time)
	}

	return frame, nil
}

func getConfigMapDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelsJSON, _ := json.Marshal(cm.Labels)
	annotationsJSON, _ := json.Marshal(cm.Annotations)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		cm.Name, cm.Namespace, string(cm.UID), cm.CreationTimestamp.Time,
		"", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"ConfigMap",
	)

	dataJSON, _ := json.Marshal(cm.Data)
	dataFrame := data.NewFrame("cm_data",
		data.NewField("Data", nil, []json.RawMessage{}),
	)
	dataFrame.AppendRow(json.RawMessage(dataJSON))

	return data.Frames{metaFrame, dataFrame}, nil
}

func getSecretDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelsJSON, _ := json.Marshal(secret.Labels)
	annotationsJSON, _ := json.Marshal(secret.Annotations)

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Type", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		secret.Name, secret.Namespace, string(secret.UID), secret.CreationTimestamp.Time,
		"", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"Secret", string(secret.Type),
	)

	dataFrame := data.NewFrame("secret_data",
		data.NewField("Key", nil, []string{}),
		data.NewField("Value", nil, []string{}),
		data.NewField("Size", nil, []int64{}),
	)
	setDetailMeta(dataFrame)
	for k, v := range secret.Data {
		dataFrame.AppendRow(k, base64.StdEncoding.EncodeToString(v), int64(len(v)))
	}

	return data.Frames{metaFrame, dataFrame}, nil
}

func getPVCDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelsJSON, _ := json.Marshal(pvc.Labels)
	annotationsJSON, _ := json.Marshal(pvc.Annotations)

	storageClass := ""
	if pvc.Spec.StorageClassName != nil {
		storageClass = *pvc.Spec.StorageClassName
	}
	capacity := ""
	if storage, ok := pvc.Status.Capacity["storage"]; ok {
		capacity = storage.String()
	}
	modes := make([]string, len(pvc.Spec.AccessModes))
	for i, m := range pvc.Spec.AccessModes {
		modes[i] = string(m)
	}
	modesJSON, _ := json.Marshal(modes)

	ownerKind, ownerName := "", ""
	if len(pvc.OwnerReferences) > 0 {
		ownerKind = pvc.OwnerReferences[0].Kind
		ownerName = pvc.OwnerReferences[0].Name
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Storage Class", nil, []string{}),
		data.NewField("Volume Name", nil, []string{}),
		data.NewField("Capacity", nil, []string{}),
		data.NewField("Access Modes", nil, []json.RawMessage{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		pvc.Name, pvc.Namespace, string(pvc.UID), pvc.CreationTimestamp.Time,
		ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"PersistentVolumeClaim",
		string(pvc.Status.Phase), storageClass, pvc.Spec.VolumeName, capacity,
		json.RawMessage(modesJSON),
	)

	return data.Frames{metaFrame}, nil
}

func getStorageClassDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	sc, err := clientset.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelsJSON, _ := json.Marshal(sc.Labels)
	annotationsJSON, _ := json.Marshal(sc.Annotations)
	parametersJSON, _ := json.Marshal(sc.Parameters)

	reclaimPolicy := ""
	if sc.ReclaimPolicy != nil {
		reclaimPolicy = string(*sc.ReclaimPolicy)
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Provisioner", nil, []string{}),
		data.NewField("Parameters", nil, []json.RawMessage{}),
		data.NewField("Reclaim Policy", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		sc.Name, string(sc.UID), sc.CreationTimestamp.Time,
		"", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"StorageClass",
		sc.Provisioner, json.RawMessage(parametersJSON), reclaimPolicy,
	)

	pvList, err := clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pvFrame := data.NewFrame("persistent_volumes",
		data.NewField("Name", nil, []string{}),
		data.NewField("Capacity", nil, []string{}),
		data.NewField("Access Modes", nil, []json.RawMessage{}),
		data.NewField("Reclaim Policy", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Claim", nil, []string{}),
		data.NewField("Storage Class", nil, []string{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
	)
	setDetailMeta(pvFrame)

	for _, pv := range pvList.Items {
		if pv.Spec.StorageClassName != name {
			continue
		}
		pvCapacity := ""
		if storage, ok := pv.Spec.Capacity["storage"]; ok {
			pvCapacity = storage.String()
		}
		pvModes := make([]string, len(pv.Spec.AccessModes))
		for i, m := range pv.Spec.AccessModes {
			pvModes[i] = string(m)
		}
		pvModesJSON, err := json.Marshal(pvModes)
		if err != nil {
			return nil, err
		}
		claim := ""
		if pv.Spec.ClaimRef != nil {
			claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
		}
		pvFrame.AppendRow(
			pv.Name, pvCapacity, json.RawMessage(pvModesJSON),
			string(pv.Spec.PersistentVolumeReclaimPolicy),
			string(pv.Status.Phase), claim, pv.Spec.StorageClassName,
			pv.Status.Reason, pv.CreationTimestamp.Time,
		)
	}

	return data.Frames{metaFrame, pvFrame}, nil
}

func getNodeDetailRich(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(node.Labels)
	annotationsJSON, _ := json.Marshal(node.Annotations)

	internalIP, externalIP, hostname := "", "", ""
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case "InternalIP":
			internalIP = addr.Address
		case "ExternalIP":
			externalIP = addr.Address
		case "Hostname":
			hostname = addr.Address
		}
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Internal IP", nil, []string{}),
		data.NewField("External IP", nil, []string{}),
		data.NewField("Hostname", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(node.Name, string(node.UID), node.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"Node", internalIP, externalIP, hostname)

	sysFrame := data.NewFrame("node_info",
		data.NewField("Machine ID", nil, []string{}),
		data.NewField("System UUID", nil, []string{}),
		data.NewField("Boot ID", nil, []string{}),
		data.NewField("Kernel Version", nil, []string{}),
		data.NewField("OS Image", nil, []string{}),
		data.NewField("Container Runtime", nil, []string{}),
		data.NewField("Kubelet Version", nil, []string{}),
		data.NewField("OS", nil, []string{}),
		data.NewField("Architecture", nil, []string{}),
	)
	setDetailMeta(sysFrame)
	sysFrame.AppendRow(
		node.Status.NodeInfo.MachineID,
		node.Status.NodeInfo.SystemUUID,
		node.Status.NodeInfo.BootID,
		node.Status.NodeInfo.KernelVersion,
		node.Status.NodeInfo.OSImage,
		node.Status.NodeInfo.ContainerRuntimeVersion,
		node.Status.NodeInfo.KubeletVersion,
		node.Status.NodeInfo.OperatingSystem,
		node.Status.NodeInfo.Architecture,
	)

	condFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Probe Time", nil, []*time.Time{}),
		data.NewField("Last Transition Time", nil, []*time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(condFrame)
	for _, c := range node.Status.Conditions {
		var lastProbe, lastTransition *time.Time
		if !c.LastHeartbeatTime.IsZero() {
			t := c.LastHeartbeatTime.Time
			lastProbe = &t
		}
		if !c.LastTransitionTime.IsZero() {
			t := c.LastTransitionTime.Time
			lastTransition = &t
		}
		condFrame.AppendRow(string(c.Type), string(c.Status), lastProbe, lastTransition, c.Reason, c.Message)
	}

	podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + name,
	})
	if err != nil {
		return nil, err
	}

	podsFrame := data.NewFrame("pods",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("IP", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
	)
	setDetailMeta(podsFrame)
	var activePodCount int
	var totalCPURequests, totalCPULimits, totalMemRequests, totalMemLimits int64
	for _, p := range podList.Items {
		podsFrame.AppendRow(p.Name, p.Namespace, string(p.Status.Phase), p.Status.PodIP, p.CreationTimestamp.Time)
		if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			activePodCount++
			for _, c := range p.Spec.Containers {
				if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
					totalCPURequests += req.MilliValue()
				}
				if lim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
					totalCPULimits += lim.MilliValue()
				}
				if req, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
					totalMemRequests += req.Value()
				}
				if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
					totalMemLimits += lim.Value()
				}
			}
		}
	}

	resourcesFrame := data.NewFrame("resources",
		data.NewField("Resource", nil, []string{}),
		data.NewField("Requests", nil, []string{}),
		data.NewField("Limits", nil, []string{}),
		data.NewField("Capacity", nil, []string{}),
		data.NewField("Allocatable", nil, []string{}),
	)
	setDetailMeta(resourcesFrame)

	cpuCap, cpuAlloc := "", ""
	if c, ok := node.Status.Capacity[corev1.ResourceCPU]; ok {
		cpuCap = c.String()
	}
	if c, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
		cpuAlloc = c.String()
	}
	memCap, memAlloc := "", ""
	if m, ok := node.Status.Capacity[corev1.ResourceMemory]; ok {
		memCap = m.String()
	}
	if m, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
		memAlloc = m.String()
	}
	podCap, podAlloc := "", ""
	if p, ok := node.Status.Capacity[corev1.ResourcePods]; ok {
		podCap = p.String()
	}
	if p, ok := node.Status.Allocatable[corev1.ResourcePods]; ok {
		podAlloc = p.String()
	}

	resourcesFrame.AppendRow("CPU", fmt.Sprintf("%dm", totalCPURequests), fmt.Sprintf("%dm", totalCPULimits), cpuCap, cpuAlloc)
	resourcesFrame.AppendRow("Memory", fmt.Sprintf("%dMi", totalMemRequests/(1024*1024)), fmt.Sprintf("%dMi", totalMemLimits/(1024*1024)), memCap, memAlloc)
	resourcesFrame.AppendRow("Pods", fmt.Sprintf("%d", activePodCount), "", podCap, podAlloc)

	return data.Frames{metaFrame, sysFrame, condFrame, resourcesFrame, podsFrame}, nil
}

func getClusterRoleBindingDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	crb, err := clientset.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(crb.Labels)
	annotationsJSON, _ := json.Marshal(crb.Annotations)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Role Reference", nil, []string{}),
		data.NewField("Role Ref Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(crb.Name, string(crb.UID), crb.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"ClusterRoleBinding", crb.RoleRef.Name, crb.RoleRef.Kind)

	subjectsFrame := data.NewFrame("subjects",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("API Group", nil, []string{}),
	)
	setDetailMeta(subjectsFrame)
	for _, s := range crb.Subjects {
		subjectsFrame.AppendRow(s.Name, s.Namespace, s.Kind, s.APIGroup)
	}
	return data.Frames{metaFrame, subjectsFrame}, nil
}

func getClusterRoleDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	cr, err := clientset.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(cr.Labels)
	annotationsJSON, _ := json.Marshal(cr.Annotations)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(cr.Name, string(cr.UID), cr.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON), "ClusterRole")

	rulesFrame := data.NewFrame("rbac_rules",
		data.NewField("Resources", nil, []string{}),
		data.NewField("Non-resource URLs", nil, []string{}),
		data.NewField("Resource Names", nil, []string{}),
		data.NewField("Verbs", nil, []string{}),
		data.NewField("API Groups", nil, []string{}),
	)
	setDetailMeta(rulesFrame)
	for _, rule := range cr.Rules {
		resources := strings.Join(rule.Resources, ", ")
		nonResourceURLs := strings.Join(rule.NonResourceURLs, ", ")
		resourceNames := strings.Join(rule.ResourceNames, ", ")
		verbs := strings.Join(rule.Verbs, ", ")
		apiGroups := strings.Join(rule.APIGroups, ", ")
		if resources == "" {
			resources = "-"
		}
		if nonResourceURLs == "" {
			nonResourceURLs = "-"
		}
		if resourceNames == "" {
			resourceNames = "-"
		}
		rulesFrame.AppendRow(resources, nonResourceURLs, resourceNames, verbs, apiGroups)
	}
	return data.Frames{metaFrame, rulesFrame}, nil
}

func getNamespaceDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(ns.Labels)
	annotationsJSON, _ := json.Marshal(ns.Annotations)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Status", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(ns.Name, string(ns.UID), ns.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"Namespace", string(ns.Status.Phase))
	return data.Frames{metaFrame}, nil
}

func getNetworkPolicyDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	np, err := clientset.NetworkingV1().NetworkPolicies(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(np.Labels)
	annotationsJSON, _ := json.Marshal(np.Annotations)
	types := make([]string, len(np.Spec.PolicyTypes))
	for i, t := range np.Spec.PolicyTypes {
		types[i] = string(t)
	}
	typesJSON, _ := json.Marshal(types)
	podSelector := metav1.FormatLabelSelector(&np.Spec.PodSelector)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Pod Selector", nil, []string{}),
		data.NewField("Policy Types", nil, []json.RawMessage{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(np.Name, np.Namespace, string(np.UID), np.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"NetworkPolicy", podSelector, json.RawMessage(typesJSON))
	return data.Frames{metaFrame}, nil
}

func getPersistentVolumeDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	pv, err := clientset.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(pv.Labels)
	annotationsJSON, _ := json.Marshal(pv.Annotations)
	modes := make([]string, len(pv.Spec.AccessModes))
	for i, m := range pv.Spec.AccessModes {
		modes[i] = string(m)
	}
	modesJSON, _ := json.Marshal(modes)
	claim := ""
	claimNamespace := ""
	claimName := ""
	if pv.Spec.ClaimRef != nil {
		claimNamespace = pv.Spec.ClaimRef.Namespace
		claimName = pv.Spec.ClaimRef.Name
		claim = claimNamespace + "/" + claimName
	}
	reclaimPolicy := string(pv.Spec.PersistentVolumeReclaimPolicy)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Claim", nil, []string{}),
		data.NewField("Claim Namespace", nil, []string{}),
		data.NewField("Claim Name", nil, []string{}),
		data.NewField("Reclaim Policy", nil, []string{}),
		data.NewField("Storage Class", nil, []string{}),
		data.NewField("Access Modes", nil, []json.RawMessage{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(pv.Name, string(pv.UID), pv.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"PersistentVolume", string(pv.Status.Phase), claim, claimNamespace, claimName,
		reclaimPolicy, pv.Spec.StorageClassName, json.RawMessage(modesJSON))

	sourceFrame := data.NewFrame("pv_source",
		data.NewField("Type", nil, []string{}),
		data.NewField("Filesystem Type", nil, []string{}),
		data.NewField("Volume ID", nil, []string{}),
	)
	setDetailMeta(sourceFrame)
	pvType := ""
	fsType := ""
	volumeID := ""
	switch {
	case pv.Spec.AWSElasticBlockStore != nil:
		pvType = "EBS (AWS Elastic Block Store)"
		fsType = pv.Spec.AWSElasticBlockStore.FSType
		volumeID = pv.Spec.AWSElasticBlockStore.VolumeID
	case pv.Spec.GCEPersistentDisk != nil:
		pvType = "GCE Persistent Disk"
		fsType = pv.Spec.GCEPersistentDisk.FSType
		volumeID = pv.Spec.GCEPersistentDisk.PDName
	case pv.Spec.HostPath != nil:
		pvType = "Host Path"
		volumeID = pv.Spec.HostPath.Path
	case pv.Spec.NFS != nil:
		pvType = "NFS"
		volumeID = pv.Spec.NFS.Server + ":" + pv.Spec.NFS.Path
	case pv.Spec.CSI != nil:
		pvType = "CSI (" + pv.Spec.CSI.Driver + ")"
		fsType = pv.Spec.CSI.FSType
		volumeID = pv.Spec.CSI.VolumeHandle
	}
	sourceFrame.AppendRow(pvType, fsType, volumeID)

	capacityFrame := data.NewFrame("pv_capacity",
		data.NewField("Resource Name", nil, []string{}),
		data.NewField("Quantity", nil, []string{}),
	)
	setDetailMeta(capacityFrame)
	for k, v := range pv.Spec.Capacity {
		capacityFrame.AppendRow(string(k), v.String())
	}
	return data.Frames{metaFrame, sourceFrame, capacityFrame}, nil
}

func getRoleBindingDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	rb, err := clientset.RbacV1().RoleBindings(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(rb.Labels)
	annotationsJSON, _ := json.Marshal(rb.Annotations)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Role Reference", nil, []string{}),
		data.NewField("Role Ref Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(rb.Name, rb.Namespace, string(rb.UID), rb.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"RoleBinding", rb.RoleRef.Name, rb.RoleRef.Kind)

	subjectsFrame := data.NewFrame("subjects",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("API Group", nil, []string{}),
	)
	setDetailMeta(subjectsFrame)
	for _, s := range rb.Subjects {
		subjectsFrame.AppendRow(s.Name, s.Namespace, s.Kind, s.APIGroup)
	}
	return data.Frames{metaFrame, subjectsFrame}, nil
}

func getRoleDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	role, err := clientset.RbacV1().Roles(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(role.Labels)
	annotationsJSON, _ := json.Marshal(role.Annotations)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(role.Name, role.Namespace, string(role.UID), role.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON), "Role")

	rulesFrame := data.NewFrame("rbac_rules",
		data.NewField("Resources", nil, []string{}),
		data.NewField("Non-resource URLs", nil, []string{}),
		data.NewField("Resource Names", nil, []string{}),
		data.NewField("Verbs", nil, []string{}),
		data.NewField("API Groups", nil, []string{}),
	)
	setDetailMeta(rulesFrame)
	for _, rule := range role.Rules {
		resources := strings.Join(rule.Resources, ", ")
		nonResourceURLs := strings.Join(rule.NonResourceURLs, ", ")
		resourceNames := strings.Join(rule.ResourceNames, ", ")
		verbs := strings.Join(rule.Verbs, ", ")
		apiGroups := strings.Join(rule.APIGroups, ", ")
		if resources == "" {
			resources = "-"
		}
		if nonResourceURLs == "" {
			nonResourceURLs = "-"
		}
		if resourceNames == "" {
			resourceNames = "-"
		}
		rulesFrame.AppendRow(resources, nonResourceURLs, resourceNames, verbs, apiGroups)
	}
	return data.Frames{metaFrame, rulesFrame}, nil
}

func getServiceAccountDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (data.Frames, error) {
	sa, err := clientset.CoreV1().ServiceAccounts(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	labelsJSON, _ := json.Marshal(sa.Labels)
	annotationsJSON, _ := json.Marshal(sa.Annotations)
	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(sa.Name, sa.Namespace, string(sa.UID), sa.CreationTimestamp.Time, "", "",
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON), "ServiceAccount")

	secretsFrame := data.NewFrame("sa_secrets",
		data.NewField("Name", nil, []string{}),
	)
	setDetailMeta(secretsFrame)
	for _, s := range sa.Secrets {
		secretsFrame.AppendRow(s.Name)
	}

	imagePullSecretsFrame := data.NewFrame("sa_image_pull_secrets",
		data.NewField("Name", nil, []string{}),
	)
	setDetailMeta(imagePullSecretsFrame)
	for _, s := range sa.ImagePullSecrets {
		imagePullSecretsFrame.AppendRow(s.Name)
	}
	return data.Frames{metaFrame, secretsFrame, imagePullSecretsFrame}, nil
}

func getCRDDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (data.Frames, error) {
	restResult := clientset.RESTClient().Get().
		AbsPath("/apis/apiextensions.k8s.io/v1/customresourcedefinitions/" + name).
		Do(ctx)
	if err := restResult.Error(); err != nil {
		return nil, err
	}
	rawBytes, err := restResult.Raw()
	if err != nil {
		return nil, err
	}

	var crd struct {
		Metadata struct {
			Name              string            `json:"name"`
			UID               string            `json:"uid"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			Group string `json:"group"`
			Scope string `json:"scope"`
			Names struct {
				Plural     string   `json:"plural"`
				Singular   string   `json:"singular"`
				Kind       string   `json:"kind"`
				ListKind   string   `json:"listKind"`
				ShortNames []string `json:"shortNames"`
			} `json:"names"`
			Versions []struct {
				Name         string `json:"name"`
				Served       bool   `json:"served"`
				Storage      bool   `json:"storage"`
				Subresources struct {
					Status *struct{} `json:"status"`
					Scale  *struct{} `json:"scale"`
				} `json:"subresources"`
			} `json:"versions"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type               string    `json:"type"`
				Status             string    `json:"status"`
				LastTransitionTime time.Time `json:"lastTransitionTime"`
				Reason             string    `json:"reason"`
				Message            string    `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(rawBytes, &crd); err != nil {
		return nil, err
	}

	labelsJSON, _ := json.Marshal(crd.Metadata.Labels)
	annotationsJSON, _ := json.Marshal(crd.Metadata.Annotations)

	// Build subresources string from first served storage version
	subresources := "-"
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			var parts []string
			if v.Subresources.Status != nil {
				parts = append(parts, "status")
			}
			if v.Subresources.Scale != nil {
				parts = append(parts, "scale")
			}
			if len(parts) > 0 {
				subresources = strings.Join(parts, ", ")
			}
			break
		}
	}

	// Version string from storage version
	version := "-"
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			version = v.Name
			break
		}
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("Version", nil, []string{}),
		data.NewField("Scope", nil, []string{}),
		data.NewField("Group", nil, []string{}),
		data.NewField("Subresources", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(
		crd.Metadata.Name, crd.Metadata.UID, crd.Metadata.CreationTimestamp,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON),
		"CustomResourceDefinition", version, crd.Spec.Scope, crd.Spec.Group, subresources,
	)

	namesFrame := data.NewFrame("crd_names",
		data.NewField("Plural", nil, []string{}),
		data.NewField("Singular", nil, []string{}),
		data.NewField("Kind", nil, []string{}),
		data.NewField("List Kind", nil, []string{}),
		data.NewField("Short Names", nil, []string{}),
	)
	setDetailMeta(namesFrame)
	shortNames := strings.Join(crd.Spec.Names.ShortNames, ", ")
	namesFrame.AppendRow(
		crd.Spec.Names.Plural,
		crd.Spec.Names.Singular,
		crd.Spec.Names.Kind,
		crd.Spec.Names.ListKind,
		shortNames,
	)

	versionsFrame := data.NewFrame("crd_versions",
		data.NewField("Name", nil, []string{}),
		data.NewField("Served", nil, []bool{}),
		data.NewField("Storage", nil, []bool{}),
	)
	setDetailMeta(versionsFrame)
	for _, v := range crd.Spec.Versions {
		versionsFrame.AppendRow(v.Name, v.Served, v.Storage)
	}

	conditionsFrame := data.NewFrame("conditions",
		data.NewField("Type", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Last Transition Time", nil, []time.Time{}),
		data.NewField("Reason", nil, []string{}),
		data.NewField("Message", nil, []string{}),
	)
	setDetailMeta(conditionsFrame)
	for _, c := range crd.Status.Conditions {
		conditionsFrame.AppendRow(c.Type, c.Status, c.LastTransitionTime, c.Reason, c.Message)
	}

	return data.Frames{metaFrame, namesFrame, versionsFrame, conditionsFrame}, nil
}

// discoverCR uses API discovery to find the GVR for a resource name (plural).
// It iterates over all API groups/versions looking for a matching resource.
// deprecatedGroups maps deprecated API groups to their preferred replacements.
// When multiple groups serve the same resource, the non-deprecated group wins.
var deprecatedGroups = map[string]bool{
	"traefik.containo.us": true,
}

func discoverCR(disc discovery.DiscoveryInterface, resourceName string) (schema.GroupVersionResource, error) {
	_, apiResourceLists, err := disc.ServerGroupsAndResources()
	if err != nil {
		// Discovery can return partial results with an error; try to use what we got
		if apiResourceLists == nil {
			return schema.GroupVersionResource{}, fmt.Errorf("API discovery failed: %v", err)
		}
	}

	var matches []schema.GroupVersionResource
	for _, list := range apiResourceLists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if r.Name == resourceName {
				matches = append(matches, schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				})
			}
		}
	}

	if len(matches) == 0 {
		return schema.GroupVersionResource{}, fmt.Errorf("resource %q not found via API discovery", resourceName)
	}

	// Prefer non-deprecated groups
	for _, m := range matches {
		if !deprecatedGroups[m.Group] {
			return m, nil
		}
	}
	return matches[0], nil
}

func listCustomResources(ctx context.Context, dynClient dynamic.Interface, gvr schema.GroupVersionResource, namespace string, listOpts metav1.ListOptions) (*data.Frame, error) {
	var rc dynamic.ResourceInterface
	if namespace == "" {
		rc = dynClient.Resource(gvr)
	} else {
		rc = dynClient.Resource(gvr).Namespace(namespace)
	}
	result, err := rc.List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("Workloads",
		data.NewField("Name", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Status", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Created", nil, []time.Time{}),
	)

	for _, item := range result.Items {
		status := ""
		if statusObj, ok := item.Object["status"].(map[string]interface{}); ok {
			if phase, ok := statusObj["phase"].(string); ok {
				status = phase
			} else if conditions, ok := statusObj["conditions"].([]interface{}); ok && len(conditions) > 0 {
				if last, ok := conditions[len(conditions)-1].(map[string]interface{}); ok {
					if t, _ := last["type"].(string); t != "" {
						s, _ := last["status"].(string)
						status = t + "=" + s
					}
				}
			}
		}

		labelsJSON, _ := json.Marshal(item.GetLabels())
		frame.AppendRow(item.GetName(), item.GetNamespace(), status, json.RawMessage(labelsJSON), item.GetCreationTimestamp().Time)
	}

	return frame, nil
}

func getCustomResourceDetail(ctx context.Context, dynClient dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) (data.Frames, error) {
	var rc dynamic.ResourceInterface
	if namespace == "" {
		rc = dynClient.Resource(gvr)
	} else {
		rc = dynClient.Resource(gvr).Namespace(namespace)
	}
	result, err := rc.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	raw := result.Object

	metadata := raw["metadata"].(map[string]interface{})
	nameStr, _ := metadata["name"].(string)
	uid, _ := metadata["uid"].(string)
	ns, _ := metadata["namespace"].(string)
	kind, _ := raw["kind"].(string)

	var created time.Time
	if ts, ok := metadata["creationTimestamp"].(string); ok {
		created, _ = time.Parse(time.RFC3339, ts)
	}

	labels := map[string]interface{}{}
	if l, ok := metadata["labels"].(map[string]interface{}); ok {
		labels = l
	}
	annotations := map[string]interface{}{}
	if a, ok := metadata["annotations"].(map[string]interface{}); ok {
		annotations = a
	}
	labelsJSON, _ := json.Marshal(labels)
	annotationsJSON, _ := json.Marshal(annotations)

	ownerKind, ownerName := "", ""
	if owners, ok := metadata["ownerReferences"].([]interface{}); ok && len(owners) > 0 {
		if owner, ok := owners[0].(map[string]interface{}); ok {
			ownerKind, _ = owner["kind"].(string)
			ownerName, _ = owner["name"].(string)
		}
	}

	metaFrame := data.NewFrame("meta",
		data.NewField("Name", nil, []string{}),
		data.NewField("UID", nil, []string{}),
		data.NewField("Namespace", nil, []string{}),
		data.NewField("Created", nil, []time.Time{}),
		data.NewField("Owner Kind", nil, []string{}),
		data.NewField("Owner Name", nil, []string{}),
		data.NewField("Labels", nil, []json.RawMessage{}),
		data.NewField("Annotations", nil, []json.RawMessage{}),
		data.NewField("Kind", nil, []string{}),
	)
	setDetailMeta(metaFrame)
	metaFrame.AppendRow(nameStr, uid, ns, created, ownerKind, ownerName,
		json.RawMessage(labelsJSON), json.RawMessage(annotationsJSON), kind)

	specFrame := data.NewFrame("spec",
		data.NewField("Key", nil, []string{}),
		data.NewField("Value", nil, []string{}),
	)
	setDetailMeta(specFrame)
	if spec, ok := raw["spec"].(map[string]interface{}); ok {
		for k, v := range spec {
			val, _ := json.Marshal(v)
			specFrame.AppendRow(k, string(val))
		}
	}

	statusFrame := data.NewFrame("status",
		data.NewField("Key", nil, []string{}),
		data.NewField("Value", nil, []string{}),
	)
	setDetailMeta(statusFrame)
	if status, ok := raw["status"].(map[string]interface{}); ok {
		for k, v := range status {
			val, _ := json.Marshal(v)
			statusFrame.AppendRow(k, string(val))
		}
	}

	return data.Frames{metaFrame, specFrame, statusFrame}, nil
}
