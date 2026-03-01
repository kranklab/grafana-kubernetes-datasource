package plugin

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ResourceCount struct {
	Total  int
	Failed int
}

type WorkloadCounts struct {
	Pods         ResourceCount
	Deployments  ResourceCount
	StatefulSets ResourceCount
	DaemonSets   ResourceCount
	CronJobs     ResourceCount
	Jobs         ResourceCount
	ReplicaSets  ResourceCount
}

func getWorkloadCounts(ctx context.Context, clientset *kubernetes.Clientset) (*WorkloadCounts, error) {
	counts := &WorkloadCounts{}

	// Get all namespaces
	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %v", err)
	}

	for _, ns := range namespaces.Items {
		// Count Pods
		pods, err := clientset.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.Pods.Total += len(pods.Items)
			for _, pod := range pods.Items {
				if pod.Status.Phase == "Failed" || pod.Status.Phase == "Unknown" {
					counts.Pods.Failed++
				}
			}
		}

		// Count Deployments
		deployments, err := clientset.AppsV1().Deployments(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.Deployments.Total += len(deployments.Items)
			for _, deploy := range deployments.Items {
				if deploy.Status.ReadyReplicas < deploy.Status.Replicas {
					counts.Deployments.Failed++
				}
			}
		}

		// Count StatefulSets
		statefulsets, err := clientset.AppsV1().StatefulSets(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.StatefulSets.Total += len(statefulsets.Items)
			for _, sts := range statefulsets.Items {
				if sts.Status.ReadyReplicas < sts.Status.Replicas {
					counts.StatefulSets.Failed++
				}
			}
		}

		// Count DaemonSets
		daemonsets, err := clientset.AppsV1().DaemonSets(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.DaemonSets.Total += len(daemonsets.Items)
			for _, ds := range daemonsets.Items {
				if ds.Status.NumberReady < ds.Status.DesiredNumberScheduled {
					counts.DaemonSets.Failed++
				}
			}
		}

		// Count CronJobs
		cronjobs, err := clientset.BatchV1().CronJobs(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.CronJobs.Total += len(cronjobs.Items)
			for _, cj := range cronjobs.Items {
				if cj.Status.LastScheduleTime == nil {
					continue
				}
				// A CronJob is considered failed if its last job failed
				if len(cj.Status.Active) > 0 {
					for _, ref := range cj.Status.Active {
						job, err := clientset.BatchV1().Jobs(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
						if err == nil && job.Status.Failed > 0 {
							counts.CronJobs.Failed++
							break
						}
					}
				}
			}
		}

		// Count Jobs
		jobs, err := clientset.BatchV1().Jobs(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.Jobs.Total += len(jobs.Items)
			for _, job := range jobs.Items {
				if job.Status.Failed > 0 {
					counts.Jobs.Failed++
				}
			}
		}

		// Count replicaSets
		rs, err := clientset.AppsV1().ReplicaSets(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			counts.ReplicaSets.Total += len(rs.Items)
			for _, rs := range rs.Items {
				if rs.Status.ReadyReplicas < rs.Status.Replicas {
					counts.ReplicaSets.Failed++
				}
			}
		}
	}

	return counts, nil
}
