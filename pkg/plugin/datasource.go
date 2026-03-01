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
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
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

func (d *Datasource) runListQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (*data.Frame, error) {

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

	namespace := qm.Namespace
	if namespace == "_all" {
		namespace = ""
	}

	switch qm.Resource {
	case "pod", "pods":
		return getPods(ctx, clientset, namespace)
	case "deployments":
		return getDeployments(ctx, clientset, namespace)
	case "daemonsets":
		return getDeamonSets(ctx, clientset, namespace)
	case "statefulsets":
		return getStatefulSets(ctx, clientset, namespace)
	case "replicasets":
		return getReplicaSets(ctx, clientset, namespace)
	case "jobs":
		return getJobs(ctx, clientset, namespace)
	case "cronjobs":
		return getCronJobs(ctx, clientset, namespace)
	case "namespaces":
		return getNamespaces(ctx, clientset)
	case "services", "svc":
		return getServices(ctx, clientset, namespace)
	case "ingresses", "ingress":
		return getIngresses(ctx, clientset, namespace)
	case "ingressclasses", "ingressclass":
		return getIngressClasses(ctx, clientset)
	case "configmaps", "configmap":
		return getConfigMaps(ctx, clientset, namespace)
	case "secrets", "secret":
		return getSecrets(ctx, clientset, namespace)
	case "persistentvolumeclaims", "pvc":
		return getPersistentVolumeClaims(ctx, clientset, namespace)
	case "storageclasses", "storageclass":
		return getStorageClasses(ctx, clientset)
	case "crds", "crd", "customresourcedefinitions":
		return getCRDs(ctx, clientset)
	case "clusterrolebindings", "clusterrolebinding":
		return getClusterRoleBindings(ctx, clientset)
	case "clusterroles", "clusterrole":
		return getClusterRoles(ctx, clientset)
	case "events", "event":
		return getEvents(ctx, clientset, namespace)
	case "networkpolicies", "networkpolicy":
		return getNetworkPolicies(ctx, clientset, namespace)
	case "nodes", "node":
		return getNodes(ctx, clientset)
	case "persistentvolumes", "pv":
		return getPersistentVolumes(ctx, clientset)
	case "rolebindings", "rolebinding":
		return getRoleBindings(ctx, clientset, namespace)
	case "roles", "role":
		return getRoles(ctx, clientset, namespace)
	case "serviceaccounts", "serviceaccount", "sa":
		return getServiceAccounts(ctx, clientset, namespace)
	}

	return nil, fmt.Errorf("resource not recognized: %s", qm.Resource)
}

func (d *Datasource) runSummaryQuery(ctx context.Context, pCtx backend.PluginContext, qm queryModel) (*data.Frame, error) {
	var jdata jsonData
	err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &jdata)
	if err != nil {
		return nil, err
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(&rest.Config{
		Host: jdata.Url,
		TLSClientConfig: rest.TLSClientConfig{
			CertFile: jdata.ClientCert,
			KeyFile:  jdata.ClientKey,
			CAFile:   jdata.CaCert,
		},
	})
	if err != nil {
		log.Fatalf("Error creating Kubernetes client: %v", err)
	}

	counts, err := getWorkloadCounts(ctx, clientset)
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("total", nil, []int32{}),
		data.NewField("failed", nil, []int32{}),
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
	clientset, err := kubernetes.NewForConfig(&rest.Config{
		Host: jdata.Url,
		TLSClientConfig: rest.TLSClientConfig{
			CertFile: jdata.ClientCert,
			KeyFile:  jdata.ClientKey,
			CAFile:   jdata.CaCert,
		},
	})
	if err != nil {
		return nil, err
	}

	wrapSingle := func(frame *data.Frame, err error) (data.Frames, error) {
		if err != nil {
			return nil, err
		}
		setDetailMeta(frame)
		return data.Frames{frame}, nil
	}

	switch qm.Resource {
	case "pod", "pods":
		return getPodDetail(ctx, clientset, qm.Namespace, qm.Name)
	case "deployments", "deployment":
		return wrapSingle(getDeploymentDetail(ctx, clientset, qm.Namespace, qm.Name))
	case "daemonsets", "daemonset":
		return wrapSingle(getDaemonSetDetail(ctx, clientset, qm.Namespace, qm.Name))
	case "statefulsets", "statefulset":
		return wrapSingle(getStatefulSetDetail(ctx, clientset, qm.Namespace, qm.Name))
	case "services", "service", "svc":
		return wrapSingle(getServiceDetail(ctx, clientset, qm.Namespace, qm.Name))
	case "nodes", "node":
		return wrapSingle(getNodeDetail(ctx, clientset, qm.Name))
	default:
		return nil, fmt.Errorf("get not supported for resource: %s", qm.Resource)
	}
}

// newDetailFrame returns a frame with "property" and "value" columns for detail views.
func newDetailFrame() *data.Frame {
	return data.NewFrame("response",
		data.NewField("property", nil, []string{}),
		data.NewField("value", nil, []string{}),
	)
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
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("uid", nil, []string{}),
		data.NewField("created", nil, []time.Time{}),
		data.NewField("ownerKind", nil, []string{}),
		data.NewField("ownerName", nil, []string{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("annotations", nil, []json.RawMessage{}),
		data.NewField("node", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("ip", nil, []string{}),
		data.NewField("qosClass", nil, []string{}),
		data.NewField("restarts", nil, []int32{}),
		data.NewField("serviceAccount", nil, []string{}),
		data.NewField("imagePullSecrets", nil, []json.RawMessage{}),
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
		data.NewField("type", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("lastProbeTime", nil, []*time.Time{}),
		data.NewField("lastTransitionTime", nil, []*time.Time{}),
		data.NewField("reason", nil, []string{}),
		data.NewField("message", nil, []string{}),
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
		data.NewField("name", nil, []string{}),
		data.NewField("reason", nil, []string{}),
		data.NewField("message", nil, []string{}),
		data.NewField("source", nil, []string{}),
		data.NewField("subObject", nil, []string{}),
		data.NewField("count", nil, []int32{}),
		data.NewField("firstSeen", nil, []time.Time{}),
		data.NewField("lastSeen", nil, []time.Time{}),
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
		data.NewField("name", nil, []string{}),
		data.NewField("image", nil, []string{}),
		data.NewField("ready", nil, []bool{}),
		data.NewField("started", nil, []bool{}),
		data.NewField("reason", nil, []string{}),
		data.NewField("env", nil, []json.RawMessage{}),
		data.NewField("command", nil, []json.RawMessage{}),
		data.NewField("args", nil, []json.RawMessage{}),
		data.NewField("mounts", nil, []json.RawMessage{}),
		data.NewField("readinessProbe", nil, []json.RawMessage{}),
		data.NewField("securityContext", nil, []json.RawMessage{}),
		data.NewField("limits", nil, []json.RawMessage{}),
		data.NewField("requests", nil, []json.RawMessage{}),
	)
	setDetailMeta(containersFrame)

	for _, c := range pod.Spec.Containers {
		st := statusMap[c.Name]

		// env
		type envEntry struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		envEntries := make([]envEntry, 0, len(c.Env))
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
			envEntries = append(envEntries, envEntry{Name: e.Name, Value: val})
		}
		envJSON, _ := json.Marshal(envEntries)

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

func getDeploymentDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (*data.Frame, error) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	frame := newDetailFrame()
	frame.AppendRow("Name", dep.Name)
	frame.AppendRow("Namespace", dep.Namespace)
	frame.AppendRow("Replicas", fmt.Sprintf("%d", dep.Status.Replicas))
	frame.AppendRow("Ready", fmt.Sprintf("%d", dep.Status.ReadyReplicas))
	frame.AppendRow("Available", fmt.Sprintf("%d", dep.Status.AvailableReplicas))
	frame.AppendRow("Updated", fmt.Sprintf("%d", dep.Status.UpdatedReplicas))
	frame.AppendRow("Strategy", string(dep.Spec.Strategy.Type))
	frame.AppendRow("Labels", labelsToString(dep.Labels))
	frame.AppendRow("Selector", labelsToString(dep.Spec.Selector.MatchLabels))
	frame.AppendRow("Created", dep.CreationTimestamp.String())

	for _, c := range dep.Spec.Template.Spec.Containers {
		frame.AppendRow("Container: "+c.Name, c.Image)
	}
	for _, cond := range dep.Status.Conditions {
		frame.AppendRow("Condition: "+string(cond.Type), string(cond.Status)+" - "+cond.Message)
	}

	return frame, nil
}

func getDaemonSetDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (*data.Frame, error) {
	ds, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	frame := newDetailFrame()
	frame.AppendRow("Name", ds.Name)
	frame.AppendRow("Namespace", ds.Namespace)
	frame.AppendRow("Desired", fmt.Sprintf("%d", ds.Status.DesiredNumberScheduled))
	frame.AppendRow("Current", fmt.Sprintf("%d", ds.Status.CurrentNumberScheduled))
	frame.AppendRow("Ready", fmt.Sprintf("%d", ds.Status.NumberReady))
	frame.AppendRow("Available", fmt.Sprintf("%d", ds.Status.NumberAvailable))
	frame.AppendRow("Labels", labelsToString(ds.Labels))
	frame.AppendRow("Selector", labelsToString(ds.Spec.Selector.MatchLabels))
	frame.AppendRow("Created", ds.CreationTimestamp.String())

	for _, c := range ds.Spec.Template.Spec.Containers {
		frame.AppendRow("Container: "+c.Name, c.Image)
	}

	return frame, nil
}

func getStatefulSetDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (*data.Frame, error) {
	sts, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	frame := newDetailFrame()
	frame.AppendRow("Name", sts.Name)
	frame.AppendRow("Namespace", sts.Namespace)
	frame.AppendRow("Replicas", fmt.Sprintf("%d", sts.Status.Replicas))
	frame.AppendRow("Ready", fmt.Sprintf("%d", sts.Status.ReadyReplicas))
	frame.AppendRow("Current", fmt.Sprintf("%d", sts.Status.CurrentReplicas))
	frame.AppendRow("Labels", labelsToString(sts.Labels))
	frame.AppendRow("Selector", labelsToString(sts.Spec.Selector.MatchLabels))
	frame.AppendRow("Created", sts.CreationTimestamp.String())

	for _, c := range sts.Spec.Template.Spec.Containers {
		frame.AppendRow("Container: "+c.Name, c.Image)
	}

	return frame, nil
}

func getServiceDetail(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (*data.Frame, error) {
	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	frame := newDetailFrame()
	frame.AppendRow("Name", svc.Name)
	frame.AppendRow("Namespace", svc.Namespace)
	frame.AppendRow("Type", string(svc.Spec.Type))
	frame.AppendRow("ClusterIP", svc.Spec.ClusterIP)
	frame.AppendRow("Labels", labelsToString(svc.Labels))
	frame.AppendRow("Selector", labelsToString(svc.Spec.Selector))
	frame.AppendRow("Created", svc.CreationTimestamp.String())

	for _, p := range svc.Spec.Ports {
		portStr := fmt.Sprintf("%d/%s", p.Port, string(p.Protocol))
		if p.NodePort != 0 {
			portStr += fmt.Sprintf(" (NodePort: %d)", p.NodePort)
		}
		frame.AppendRow("Port: "+p.Name, portStr)
	}

	return frame, nil
}

func getNodeDetail(ctx context.Context, clientset *kubernetes.Clientset, name string) (*data.Frame, error) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	frame := newDetailFrame()
	frame.AppendRow("Name", node.Name)
	frame.AppendRow("Kubelet Version", node.Status.NodeInfo.KubeletVersion)
	frame.AppendRow("OS", node.Status.NodeInfo.OSImage)
	frame.AppendRow("Kernel", node.Status.NodeInfo.KernelVersion)
	frame.AppendRow("Container Runtime", node.Status.NodeInfo.ContainerRuntimeVersion)
	frame.AppendRow("Architecture", node.Status.NodeInfo.Architecture)
	frame.AppendRow("Labels", labelsToString(node.Labels))
	frame.AppendRow("Created", node.CreationTimestamp.String())

	for _, cond := range node.Status.Conditions {
		frame.AppendRow("Condition: "+string(cond.Type), string(cond.Status))
	}
	if cpu, ok := node.Status.Capacity["cpu"]; ok {
		frame.AppendRow("CPU Capacity", cpu.String())
	}
	if mem, ok := node.Status.Capacity["memory"]; ok {
		frame.AppendRow("Memory Capacity", mem.String())
	}

	return frame, nil
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

// getNamespaces retrieves all namespaces in the cluster
func getNamespaces(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
	)

	for _, ns := range list.Items {
		frame.AppendRow(ns.Name)
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
		data.NewField("active", nil, []int32{}),
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

		frame.AppendRow(itm.Name, itm.Namespace, "ok", json.RawMessage(jsonImages), json.RawMessage(labels), itm.Spec.Schedule, itm.Spec.Suspend, int32(len(itm.Status.Active)), lastScheduled, itm.CreationTimestamp.Time)
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
		data.NewField("images", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("node", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("restarts", nil, []int32{}),
		data.NewField("created", nil, []time.Time{}),
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
		var totalRestarts int32
		for _, containerStatus := range pod.Status.ContainerStatuses {
			totalRestarts += containerStatus.RestartCount
		}
		frame.AppendRow(pod.Name, pod.Namespace, json.RawMessage(jsonImages), json.RawMessage(labels), pod.Spec.NodeName, string(pod.Status.Phase), totalRestarts, pod.CreationTimestamp.Time)
	}

	return frame, nil
}

//func describePod(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
//
//	list, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
//	if err != nil {
//		return nil, err
//	}
//
//	frame := data.NewFrame("response",
//		data.NewField("name", nil, []string{}),
//		data.NewField("namespace", nil, []string{}),
//		data.NewField("status", nil, []string{}),
//		data.NewField("images", nil, []json.RawMessage{}),
//		data.NewField("labels", nil, []json.RawMessage{}),
//		data.NewField("target", nil, []int32{}),
//		data.NewField("available", nil, []int32{}),
//		data.NewField("created", nil, []time.Time{}),
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

func getServices(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("type", nil, []string{}),
		data.NewField("clusterip", nil, []string{}),
		data.NewField("ports", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(svc.Name, svc.Namespace, string(svc.Spec.Type), svc.Spec.ClusterIP, json.RawMessage(jsonPorts), json.RawMessage(labels), svc.CreationTimestamp.Time)
	}

	return frame, nil
}

func getIngresses(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("class", nil, []string{}),
		data.NewField("hosts", nil, []json.RawMessage{}),
		data.NewField("tls", nil, []bool{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(ing.Name, ing.Namespace, className, json.RawMessage(jsonHosts), len(ing.Spec.TLS) > 0, json.RawMessage(labels), ing.CreationTimestamp.Time)
	}

	return frame, nil
}

func getIngressClasses(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("controller", nil, []string{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
	)

	for _, ic := range list.Items {
		labels, err := json.Marshal(ic.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(ic.Name, ic.Spec.Controller, json.RawMessage(labels), ic.CreationTimestamp.Time)
	}

	return frame, nil
}

func getConfigMaps(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("keys", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(cm.Name, cm.Namespace, json.RawMessage(jsonKeys), json.RawMessage(labels), cm.CreationTimestamp.Time)
	}

	return frame, nil
}

func getSecrets(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("type", nil, []string{}),
		data.NewField("keys", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(secret.Name, secret.Namespace, string(secret.Type), json.RawMessage(jsonKeys), json.RawMessage(labels), secret.CreationTimestamp.Time)
	}

	return frame, nil
}

func getPersistentVolumeClaims(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("storageclass", nil, []string{}),
		data.NewField("capacity", nil, []string{}),
		data.NewField("accessmodes", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(pvc.Name, pvc.Namespace, string(pvc.Status.Phase), storageClass, capacity, json.RawMessage(jsonModes), json.RawMessage(labels), pvc.CreationTimestamp.Time)
	}

	return frame, nil
}

func getStorageClasses(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("provisioner", nil, []string{}),
		data.NewField("reclaimpolicy", nil, []string{}),
		data.NewField("volumebindingmode", nil, []string{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(sc.Name, sc.Provisioner, reclaimPolicy, bindingMode, json.RawMessage(labels), sc.CreationTimestamp.Time)
	}

	return frame, nil
}

func getCRDs(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	rawBytes, err := clientset.RESTClient().Get().
		AbsPath("/apis/apiextensions.k8s.io/v1/customresourcedefinitions").
		DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Labels            map[string]string `json:"labels"`
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

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("group", nil, []string{}),
		data.NewField("kind", nil, []string{}),
		data.NewField("scope", nil, []string{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
	)

	for _, crd := range result.Items {
		labels, err := json.Marshal(crd.Metadata.Labels)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(crd.Metadata.Name, crd.Spec.Group, crd.Spec.Names.Kind, crd.Spec.Scope, json.RawMessage(labels), crd.Metadata.CreationTimestamp)
	}

	return frame, nil
}

func getClusterRoleBindings(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("role", nil, []string{}),
		data.NewField("subjects", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		roleRef := crb.RoleRef.Kind + "/" + crb.RoleRef.Name
		frame.AppendRow(crb.Name, roleRef, json.RawMessage(jsonSubjects), json.RawMessage(labels), crb.CreationTimestamp.Time)
	}

	return frame, nil
}

func getClusterRoles(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("rules", nil, []int32{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
	)

	for _, cr := range list.Items {
		labels, err := json.Marshal(cr.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(cr.Name, int32(len(cr.Rules)), json.RawMessage(labels), cr.CreationTimestamp.Time)
	}

	return frame, nil
}

func getEvents(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("type", nil, []string{}),
		data.NewField("reason", nil, []string{}),
		data.NewField("object", nil, []string{}),
		data.NewField("message", nil, []string{}),
		data.NewField("count", nil, []int32{}),
		data.NewField("created", nil, []time.Time{}),
	)

	for _, ev := range list.Items {
		regarding := ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name
		frame.AppendRow(ev.Name, ev.Namespace, ev.Type, ev.Reason, regarding, ev.Message, ev.Count, ev.CreationTimestamp.Time)
	}

	return frame, nil
}

func getNetworkPolicies(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("podselector", nil, []string{}),
		data.NewField("policytypes", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(np.Name, np.Namespace, podSelector, json.RawMessage(jsonTypes), json.RawMessage(labels), np.CreationTimestamp.Time)
	}

	return frame, nil
}

func getNodes(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("roles", nil, []string{}),
		data.NewField("version", nil, []string{}),
		data.NewField("os", nil, []string{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(node.Name, status, strings.Join(roles, ","), node.Status.NodeInfo.KubeletVersion, node.Status.NodeInfo.OSImage, json.RawMessage(labels), node.CreationTimestamp.Time)
	}

	return frame, nil
}

func getPersistentVolumes(ctx context.Context, clientset *kubernetes.Clientset) (*data.Frame, error) {
	list, err := clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("status", nil, []string{}),
		data.NewField("storageclass", nil, []string{}),
		data.NewField("capacity", nil, []string{}),
		data.NewField("accessmodes", nil, []json.RawMessage{}),
		data.NewField("reclaimpolicy", nil, []string{}),
		data.NewField("claim", nil, []string{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		frame.AppendRow(pv.Name, string(pv.Status.Phase), pv.Spec.StorageClassName, capacity, json.RawMessage(jsonModes), string(pv.Spec.PersistentVolumeReclaimPolicy), claim, json.RawMessage(labels), pv.CreationTimestamp.Time)
	}

	return frame, nil
}

func getRoleBindings(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.RbacV1().RoleBindings(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("role", nil, []string{}),
		data.NewField("subjects", nil, []json.RawMessage{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
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
		roleRef := rb.RoleRef.Kind + "/" + rb.RoleRef.Name
		frame.AppendRow(rb.Name, rb.Namespace, roleRef, json.RawMessage(jsonSubjects), json.RawMessage(labels), rb.CreationTimestamp.Time)
	}

	return frame, nil
}

func getRoles(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.RbacV1().Roles(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("rules", nil, []int32{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
	)

	for _, role := range list.Items {
		labels, err := json.Marshal(role.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(role.Name, role.Namespace, int32(len(role.Rules)), json.RawMessage(labels), role.CreationTimestamp.Time)
	}

	return frame, nil
}

func getServiceAccounts(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (*data.Frame, error) {
	list, err := clientset.CoreV1().ServiceAccounts(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	frame := data.NewFrame("response",
		data.NewField("name", nil, []string{}),
		data.NewField("namespace", nil, []string{}),
		data.NewField("secrets", nil, []int32{}),
		data.NewField("labels", nil, []json.RawMessage{}),
		data.NewField("created", nil, []time.Time{}),
	)

	for _, sa := range list.Items {
		labels, err := json.Marshal(sa.ObjectMeta.Labels)
		if err != nil {
			return nil, err
		}
		frame.AppendRow(sa.Name, sa.Namespace, int32(len(sa.Secrets)), json.RawMessage(labels), sa.CreationTimestamp.Time)
	}

	return frame, nil
}
