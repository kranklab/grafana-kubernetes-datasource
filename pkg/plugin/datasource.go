package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/kranklab/kubernetes-datasource/pkg/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"log"
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
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
}

type jsonData struct {
	Url        string `json:"url"`
	ClientCert string `json:"clientCert"`
	ClientKey  string `json:"clientKey"`
	CaCert     string `json:"caCert"`
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
	case "get":
		frame, err := d.runGetQuery(ctx, pCtx, qm)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("request failed: %v", err.Error()))
		}
		response.Frames = append(response.Frames, frame)
	}

	return response
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (d *Datasource) CheckHealth(_ context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	res := &backend.CheckHealthResult{}
	config, err := models.LoadPluginSettings(*req.PluginContext.DataSourceInstanceSettings)

	if err != nil {
		res.Status = backend.HealthStatusError
		res.Message = "Unable to load settings"
		return res, nil
	}

	if config.Secrets.ApiKey == "" {
		res.Status = backend.HealthStatusError
		res.Message = "API key is missing"
		return res, nil
	}

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}

func (d *Datasource) runGetQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (*data.Frame, error) {

	var data jsonData
	err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &data)
	if err != nil {
		return nil, err
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(&rest.Config{
		Host: data.Url,
		TLSClientConfig: rest.TLSClientConfig{
			CertFile: data.ClientCert,
			KeyFile:  data.ClientKey,
			CAFile:   data.CaCert,
		},
	})
	if err != nil {
		log.Fatalf("Error creating Kubernetes client: %v", err)
	}

	switch qm.Resource {
	case "pod", "pods":
		return getPods(ctx, clientset, qm.Namespace)
	case "deployments":
		return getDeployments(ctx, clientset, qm.Namespace)
	case "daemonsets":
		return getDeamonSets(ctx, clientset, qm.Namespace)
	case "statefulsets":
		return getStatefulSets(ctx, clientset, qm.Namespace)
	case "replicasets":
		return getReplicaSets(ctx, clientset, qm.Namespace)
	case "jobs":
		return getJobs(ctx, clientset, qm.Namespace)
	case "cronjobs":
		return getCronJobs(ctx, clientset, qm.Namespace)
	}

	return nil, fmt.Errorf("resource not recognized: %s", qm.Resource)
}

func getDeployments(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("target", nil, []int32{}),
		data.NewField("available", nil, []int32{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getDaemonsets retrieves daemonsets
func getDeamonSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("target", nil, []int32{}),
		data.NewField("available", nil, []int32{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), pod.Status.DesiredNumberScheduled, pod.Status.CurrentNumberScheduled, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getStatefulsets retrieves statefulsets
func getStatefulSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("target", nil, []int32{}),
		data.NewField("available", nil, []int32{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getReplicasets retrieves replicasets
func getReplicaSets(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("target", nil, []int32{}),
		data.NewField("available", nil, []int32{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(pod.Name, pod.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), pod.Status.Replicas, pod.Status.AvailableReplicas, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

// getJobs retrieves kubernetes jobs
func getJobs(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("succeeded", nil, []int32{}),
		data.NewField("completed", nil, []*int32{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(itm.Name, itm.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), itm.Status.Succeeded, itm.Spec.Completions, itm.CreationTimestamp.Time)
	}

	return frame, nil
}

// getCronJobs retrieves kubernetes jobs
func getCronJobs(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("schedule", nil, []string{}),
		data.NewField("suspend", nil, []*bool{}),
		data.NewField("active", nil, []int{}),
		data.NewField("lastscheduled", nil, []*time.Time{}),
		data.NewField("created", nil, []time.Time{}),
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

		// Handle LastScheduleTime (use zero time if nil)
		var lastScheduled *time.Time
		if itm.Status.LastScheduleTime != nil {
			lastScheduled = &itm.Status.LastScheduleTime.Time
		}

		frame.AppendRow(itm.Name, itm.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), itm.Spec.Schedule, itm.Spec.Suspend, len(itm.Status.Active), lastScheduled, itm.CreationTimestamp.Time)
	}

	return frame, nil
}

// getPods retrieves pods from the specified namespace (or all namespaces if namespace is empty)
func getPods(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {

	list, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
	)

	for _, pod := range list.Items {
		frame.AppendRow(pod.Name, pod.Namespace)
	}

	return frame, nil
}
