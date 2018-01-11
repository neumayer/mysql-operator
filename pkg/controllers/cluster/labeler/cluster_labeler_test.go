package labeler

import (
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informers "k8s.io/client-go/informers"
	fake "k8s.io/client-go/kubernetes/fake"
	testingutil "k8s.io/client-go/testing"

	cluster "github.com/oracle/mysql-operator/pkg/cluster"
	innodb "github.com/oracle/mysql-operator/pkg/cluster/innodb"
	constants "github.com/oracle/mysql-operator/pkg/constants"
	controllerutil "github.com/oracle/mysql-operator/pkg/controllers/util"
)

func alwaysReady() bool { return true }

func newLocalInstance(ordinal int) *cluster.Instance {
	return cluster.NewInstance(metav1.NamespaceDefault, "test-cluster", "test-cluster", ordinal, 3306)
}

func newFakeClusterLabelerController(instance *cluster.Instance, pods []corev1.Pod) (*fake.Clientset, *ClusterLabelerController) {
	client := fake.NewSimpleClientset(&corev1.PodList{Items: pods})
	informerFactory := informers.NewSharedInformerFactory(client, controllerutil.NoResyncPeriodFunc())
	podInformer := informerFactory.Core().V1().Pods()

	// Fill the lister.
	for _, pod := range pods {
		podInformer.Informer().GetStore().Add(pod.DeepCopy())
	}

	controller := NewClusterLabelerController(instance, client, podInformer)
	controller.podListerSynced = alwaysReady
	return client, controller
}

func fakeWorker(clc *ClusterLabelerController) {
	if obj, done := clc.queue.Get(); !done {
		clc.syncHandler(obj.(string))
		clc.queue.Done(obj)
	}
}

func TestClusterLabelerLabelsPrimaryAndSecondaries(t *testing.T) {
	pods := []corev1.Pod{
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-0",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
				},
			},
		},
	}
	status := innodb.ClusterStatus{
		ClusterName: "MySQLCluster",
		DefaultReplicaSet: innodb.ReplicaSet{
			Name:       "default",
			Primary:    "test-cluster-0:3306",
			Status:     "OK",
			StatusText: "Cluster is ONLINE and can tolerate up to ONE failure.",
			Topology: map[string]*innodb.Instance{
				"test-cluster-0:3306": &innodb.Instance{
					Address: "test-cluster-0:3306",
					Mode:    "R/W",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-1:3306": &innodb.Instance{
					Address: "test-cluster-1:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-2:3306": &innodb.Instance{
					Address: "test-cluster-2:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
			},
		},
	}

	client, controller := newFakeClusterLabelerController(newLocalInstance(0), pods)
	controller.EnqueueClusterStatus(status.DeepCopy())
	fakeWorker(controller)

	actions := client.Actions()

	if len(actions) != 3 {
		t.Fatalf("Expected 3 actions but got %d: %+v", len(actions), actions)
	}

	// Check test-cluster-0 labeled as primary
	pod, err := getPodFromPatchAction(actions[0])
	if err != nil {
		t.Fatal(err)
	}
	role, ok := pod.Labels[LabelMySQLClusterRole]
	if !ok || role != MySQLClusterRolePrimary {
		t.Errorf("test-cluster-0 not labeled as primary labels=%+v", pod.Labels)
	}

	// Check test-cluster-1 labeled as secondary
	pod, err = getPodFromPatchAction(actions[1])
	if err != nil {
		t.Fatal(err)
	}
	role, ok = pod.Labels[LabelMySQLClusterRole]
	if !ok || role != MySQLClusterRoleSecondary {
		t.Errorf("test-cluster-1 not labeled as secondary labels=%+v", pod.Labels)
	}

	// Check test-cluster-2 labeled as secondary
	pod, err = getPodFromPatchAction(actions[2])
	if err != nil {
		t.Fatal(err)
	}
	role, ok = pod.Labels[LabelMySQLClusterRole]
	if !ok || role != MySQLClusterRoleSecondary {
		t.Errorf("test-cluster-1 not labeled as secondary labels=%+v", pod.Labels)
	}
}

func TestClusterLabelerRelabelsOldPrimary(t *testing.T) {
	pods := []corev1.Pod{
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-0",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRolePrimary,
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRoleSecondary,
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRoleSecondary,
				},
			},
		},
	}
	status := innodb.ClusterStatus{
		ClusterName: "MySQLCluster",
		DefaultReplicaSet: innodb.ReplicaSet{
			Name:       "default",
			Primary:    "test-cluster-1:3306",
			Status:     "OK",
			StatusText: "Cluster is ONLINE and can tolerate up to ONE failure.",
			Topology: map[string]*innodb.Instance{
				"test-cluster-0:3306": &innodb.Instance{
					Address: "test-cluster-0:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-1:3306": &innodb.Instance{
					Address: "test-cluster-1:3306",
					Mode:    "R/W",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-2:3306": &innodb.Instance{
					Address: "test-cluster-2:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
			},
		},
	}

	client, controller := newFakeClusterLabelerController(newLocalInstance(1), pods)
	controller.EnqueueClusterStatus(status.DeepCopy())
	fakeWorker(controller)

	actions := client.Actions()

	if len(actions) != 2 {
		t.Fatalf("Expected 2 actions but got %d: %+v", len(actions), actions)
	}

	// Check test-cluster-0 labeled as secondary
	pod, err := getPodFromPatchAction(actions[0])
	if err != nil {
		t.Fatal(err)
	}
	role, ok := pod.Labels[LabelMySQLClusterRole]
	if !ok || role != MySQLClusterRoleSecondary {
		t.Errorf("test-cluster-0 not labeled as secondary labels=%+v", pod.Labels)
	}

	// Check test-cluster-1 labeled as primary
	pod, err = getPodFromPatchAction(actions[1])
	if err != nil {
		t.Fatal(err)
	}
	role, ok = pod.Labels[LabelMySQLClusterRole]
	if !ok || role != MySQLClusterRolePrimary {
		t.Errorf("test-cluster-1 not labeled as primary labels=%+v", pod.Labels)
	}
}

func TestClusterLabelerDoesntRelabelCorrectlyLabeledPods(t *testing.T) {
	pods := []corev1.Pod{
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-0",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRolePrimary,
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRoleSecondary,
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRoleSecondary,
				},
			},
		},
	}
	status := innodb.ClusterStatus{
		ClusterName: "MySQLCluster",
		DefaultReplicaSet: innodb.ReplicaSet{
			Name:       "default",
			Primary:    "test-cluster-0:3306",
			Status:     "OK",
			StatusText: "Cluster is ONLINE and can tolerate up to ONE failure.",
			Topology: map[string]*innodb.Instance{
				"test-cluster-0:3306": &innodb.Instance{
					Address: "test-cluster-0:3306",
					Mode:    "R/W",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-1:3306": &innodb.Instance{
					Address: "test-cluster-1:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-2:3306": &innodb.Instance{
					Address: "test-cluster-2:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
			},
		},
	}

	client, controller := newFakeClusterLabelerController(newLocalInstance(0), pods)
	controller.EnqueueClusterStatus(status.DeepCopy())
	fakeWorker(controller)

	actions := client.Actions()

	if len(actions) != 0 {
		t.Fatalf("Expected 0 actions but got %d: %+v", len(actions), actions)
	}
}

func TestClusterLabelerRemovesLabelFromInstanceInMissingState(t *testing.T) {
	pods := []corev1.Pod{
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-0",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRolePrimary,
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRoleSecondary,
				},
			},
		},
		corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					constants.MySQLClusterLabel: "test-cluster",
					LabelMySQLClusterRole:       MySQLClusterRoleSecondary,
				},
			},
		},
	}
	status := innodb.ClusterStatus{
		ClusterName: "MySQLCluster",
		DefaultReplicaSet: innodb.ReplicaSet{
			Name:       "default",
			Primary:    "test-cluster-0:3306",
			Status:     "OK",
			StatusText: "Cluster is ONLINE and can tolerate up to ONE failure.",
			Topology: map[string]*innodb.Instance{
				"test-cluster-0:3306": &innodb.Instance{
					Address: "test-cluster-0:3306",
					Mode:    "R/W",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-1:3306": &innodb.Instance{
					Address: "test-cluster-1:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusOnline,
				},
				"test-cluster-2:3306": &innodb.Instance{
					Address: "test-cluster-2:3306",
					Mode:    "R/O",
					Role:    "HA",
					Status:  innodb.InstanceStatusMissing,
				},
			},
		},
	}

	client, controller := newFakeClusterLabelerController(newLocalInstance(0), pods)
	controller.EnqueueClusterStatus(status.DeepCopy())
	fakeWorker(controller)

	actions := client.Actions()

	if len(actions) != 1 {
		t.Fatalf("Expected 1 actions but got %d: %+v", len(actions), actions)
	}

	// Check label removed from test-cluster-2
	pod, err := getPodFromPatchAction(actions[0])
	if err != nil {
		t.Fatal(err)
	}
	role, _ := pod.Labels[LabelMySQLClusterRole]
	if role != "" {
		t.Errorf("label not removed from test-cluster-2 labels=%+v", pod.Labels)
	}
}

func getPodFromPatchAction(action testingutil.Action) (*corev1.Pod, error) {
	if action.GetVerb() == "patch" && action.GetResource().Resource == "pods" {
		patchAction, ok := action.(testingutil.PatchAction)
		if !ok {
			return nil, fmt.Errorf("action %+v is not a patch", action)
		}

		pod := &corev1.Pod{}
		err := json.Unmarshal(patchAction.GetPatch(), pod)
		if err != nil {
			return nil, err
		}

		return pod, nil
	}

	return nil, fmt.Errorf("expected PATCH Pod to be sent to client, got this action instead: %v", action)
}