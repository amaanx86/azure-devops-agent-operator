/*
Copyright 2026 Amaan Ul Haq Siddiqui.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	agentsv1alpha1 "github.com/amaanx86/azure-devops-agent-operator/api/v1alpha1"
	"github.com/amaanx86/azure-devops-agent-operator/internal/azuredevops"
)

// fakeADOClient is an in-memory ADO client for tests.
type fakeADOClient struct {
	poolID            int
	poolIDErr         error
	jobStatus         azuredevops.JobStatus
	jobStatusErr      error
	dummyIDCounter    int
	registeredDummies map[string]int
	unregisteredIDs   []int
	agentsByName      map[string]int
}

func newFakeADOClient(poolID int) *fakeADOClient {
	return &fakeADOClient{
		poolID:            poolID,
		jobStatus:         azuredevops.JobStatus{BusyAgentNames: make(map[string]bool)},
		registeredDummies: make(map[string]int),
		agentsByName:      make(map[string]int),
	}
}

func (f *fakeADOClient) GetPoolID(_ context.Context, _, _ string) (int, error) {
	return f.poolID, f.poolIDErr
}

func (f *fakeADOClient) GetJobStatus(_ context.Context, _ string, _ int) (azuredevops.JobStatus, error) {
	return f.jobStatus, f.jobStatusErr
}

func (f *fakeADOClient) RegisterDummyAgent(_ context.Context, _ string, _ int, name string) (int, error) {
	f.dummyIDCounter++
	id := f.dummyIDCounter
	f.registeredDummies[name] = id
	f.agentsByName[name] = id
	return id, nil
}

func (f *fakeADOClient) UnregisterAgent(_ context.Context, _ string, _, agentID int) error {
	f.unregisteredIDs = append(f.unregisteredIDs, agentID)
	for name, id := range f.agentsByName {
		if id == agentID {
			delete(f.agentsByName, name)
			break
		}
	}
	return nil
}

func (f *fakeADOClient) GetAgentByName(_ context.Context, _ string, _ int, name string) (int, error) {
	return f.agentsByName[name], nil
}

// reconcilerWithFake builds a reconciler using the given fake ADO client.
func reconcilerWithFake(fake *fakeADOClient) *AgentPoolReconciler {
	return &AgentPoolReconciler{
		Client:       k8sClient,
		Scheme:       k8sClient.Scheme(),
		Recorder:     newFakeRecorder(),
		newADOClient: func(_ string) adoClientFace { return fake },
	}
}

type fakeRecorder struct{}

func newFakeRecorder() *fakeRecorder { return &fakeRecorder{} }

func (f *fakeRecorder) Event(_ runtime.Object, _, _, _ string)            {}
func (f *fakeRecorder) Eventf(_ runtime.Object, _, _, _ string, _ ...any) {}
func (f *fakeRecorder) AnnotatedEventf(_ runtime.Object, _ map[string]string, _, _, _ string, _ ...any) {
}

// testPool creates a minimal AgentPool spec for tests.
//
//nolint:unparam
func testPool(name, namespace string) *agentsv1alpha1.AgentPool {
	return &agentsv1alpha1.AgentPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentPoolSpec{
			OrganizationURL: "https://dev.azure.com/testorg",
			PoolName:        "test-pool",
			TokenSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "test-token"},
				Key:                  "token",
			},
			MinAgents: 0,
			MaxAgents: 5,
		},
	}
}

//nolint:unparam
func testSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{"token": []byte("dummy-pat")},
	}
}

var _ = Describe("AgentPool Controller", func() {
	const ns = "default"
	ctx := context.Background()

	ensureSecret := func() {
		s := &corev1.Secret{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-token", Namespace: ns}, s)
		if errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, testSecret("test-token", ns))).To(Succeed())
		}
	}

	reconcile := func(r *AgentPoolReconciler, name string) (reconcile.Result, error) {
		return r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
		})
	}

	Context("Finalizer management", func() {
		It("adds the finalizer on first reconcile", func() {
			ensureSecret()
			pool := testPool("finalizer-test", ns)
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pool) }()

			fake := newFakeADOClient(42)
			r := reconcilerWithFake(fake)

			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(finalizerName))
		})
	})

	Context("Pool ID resolution", func() {
		It("caches the pool ID in status", func() {
			ensureSecret()
			pool := testPool("poolid-test", ns)
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pool) }()

			fake := newFakeADOClient(99)
			r := reconcilerWithFake(fake)

			// First call: adds finalizer
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			// Second call: resolves pool ID
			_, err = reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			Expect(updated.Status.PoolID).To(Equal(int32(99)))
		})

		It("sets Degraded condition when pool not found", func() {
			ensureSecret()
			pool := testPool("poolid-fail-test", ns)
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pool) }()

			fake := newFakeADOClient(0)
			fake.poolIDErr = fmt.Errorf("pool not found")
			r := reconcilerWithFake(fake)

			// First call: adds finalizer
			_, _ = reconcile(r, pool.Name)
			// Second call: fails to resolve pool ID
			result, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(reconcileInterval))

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			cond := findCondition(updated.Status.Conditions, "Available")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("PoolResolutionFailed"))
		})
	})

	Context("Scale-up", func() {
		It("creates pods equal to pending job count", func() {
			ensureSecret()
			pool := testPool("scaleup-test", ns)
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, pool)
				podList := &corev1.PodList{}
				_ = k8sClient.List(ctx, podList,
					client.InNamespace(ns),
					client.MatchingLabels{labelPoolName: pool.Name})
				for i := range podList.Items {
					_ = k8sClient.Delete(ctx, &podList.Items[i])
				}
			}()

			fake := newFakeADOClient(1)
			fake.jobStatus.Pending = 3
			r := reconcilerWithFake(fake)

			// Finalizer pass
			_, _ = reconcile(r, pool.Name)
			// Resolve pool ID
			_, _ = reconcile(r, pool.Name)
			// Scale up
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList,
				client.InNamespace(ns),
				client.MatchingLabels{labelPoolName: pool.Name})).To(Succeed())
			Expect(podList.Items).To(HaveLen(3))
		})

		It("does not exceed maxAgents", func() {
			ensureSecret()
			pool := testPool("maxagents-test", ns)
			pool.Spec.MaxAgents = 2
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, pool)
				podList := &corev1.PodList{}
				_ = k8sClient.List(ctx, podList,
					client.InNamespace(ns),
					client.MatchingLabels{labelPoolName: pool.Name})
				for i := range podList.Items {
					_ = k8sClient.Delete(ctx, &podList.Items[i])
				}
			}()

			fake := newFakeADOClient(1)
			fake.jobStatus.Pending = 10
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList,
				client.InNamespace(ns),
				client.MatchingLabels{labelPoolName: pool.Name})).To(Succeed())
			Expect(podList.Items).To(HaveLen(2))
		})
	})

	Context("Scale-down", func() {
		It("does not delete pods executing a job in ADO", func() {
			ensureSecret()
			pool := testPool("scaledown-busy-test", ns)
			pool.Spec.MinAgents = 0
			pool.Spec.MaxAgents = 3
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, pool)
				podList := &corev1.PodList{}
				_ = k8sClient.List(ctx, podList,
					client.InNamespace(ns),
					client.MatchingLabels{labelPoolName: pool.Name})
				for i := range podList.Items {
					_ = k8sClient.Delete(ctx, &podList.Items[i])
				}
			}()

			fake := newFakeADOClient(1)
			fake.jobStatus.Pending = 2
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			// Scale up to 2 pods
			_, _ = reconcile(r, pool.Name)

			podList := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList,
				client.InNamespace(ns),
				client.MatchingLabels{labelPoolName: pool.Name})).To(Succeed())
			Expect(podList.Items).To(HaveLen(2))

			// One pod is busy, desired drops to 0 but we should only delete the idle one.
			busyPodName := podList.Items[0].Name
			fake.jobStatus.Pending = 1
			fake.jobStatus.BusyAgentNames = map[string]bool{busyPodName: true}

			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			podList2 := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList2,
				client.InNamespace(ns),
				client.MatchingLabels{labelPoolName: pool.Name})).To(Succeed())
			// The busy pod should survive; the idle one should be deleted.
			names := make([]string, 0, len(podList2.Items))
			for _, p := range podList2.Items {
				names = append(names, p.Name)
			}
			Expect(names).To(ContainElement(busyPodName))
		})
	})

	Context("Dummy agent", func() {
		It("registers dummy when desired is 0", func() {
			ensureSecret()
			pool := testPool("dummy-register-test", ns)
			pool.Spec.MinAgents = 0
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pool) }()

			fake := newFakeADOClient(1)
			fake.jobStatus.Pending = 0
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			Expect(fake.registeredDummies).To(HaveKey(pool.Name + "-dummy"))

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			Expect(updated.Status.DummyAgentID).NotTo(BeZero())
		})

		It("unregisters dummy when jobs arrive", func() {
			ensureSecret()
			pool := testPool("dummy-unregister-test", ns)
			pool.Spec.MinAgents = 0
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, pool)
				podList := &corev1.PodList{}
				_ = k8sClient.List(ctx, podList,
					client.InNamespace(ns),
					client.MatchingLabels{labelPoolName: pool.Name})
				for i := range podList.Items {
					_ = k8sClient.Delete(ctx, &podList.Items[i])
				}
			}()

			fake := newFakeADOClient(1)
			fake.jobStatus.Pending = 0
			r := reconcilerWithFake(fake)

			// Register dummy
			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			dummyID := int(updated.Status.DummyAgentID)
			Expect(dummyID).NotTo(BeZero())

			// Jobs arrive - should unregister dummy
			fake.jobStatus.Pending = 1
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.unregisteredIDs).To(ContainElement(dummyID))
		})
	})

	Context("Status conditions", func() {
		It("sets Available=True after successful reconcile", func() {
			ensureSecret()
			pool := testPool("conditions-test", ns)
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pool) }()

			fake := newFakeADOClient(1)
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			cond := findCondition(updated.Status.Conditions, "Available")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("sets Available=False on ADO query failure", func() {
			ensureSecret()
			pool := testPool("ado-fail-test", ns)
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pool) }()

			fake := newFakeADOClient(1)
			fake.jobStatusErr = fmt.Errorf("ADO unreachable")
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			result, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(reconcileInterval))

			updated := &agentsv1alpha1.AgentPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: ns}, updated)).To(Succeed())
			cond := findCondition(updated.Status.Conditions, "Available")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Context("PVC pool", func() {
		It("pre-provisions PVCs for cache volumes", func() {
			ensureSecret()
			pool := testPool("pvc-pool-test", ns)
			pool.Spec.MaxAgents = 3
			pool.Spec.CacheVolumes = []agentsv1alpha1.CacheVolumeTemplate{
				{
					Name:      "buildcache",
					MountPath: "/cache",
					Size:      resource.MustParse("10Gi"),
				},
			}
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, pool)
				pvcList := &corev1.PersistentVolumeClaimList{}
				_ = k8sClient.List(ctx, pvcList,
					client.InNamespace(ns),
					client.MatchingLabels{labelPoolName: pool.Name})
				for i := range pvcList.Items {
					_ = k8sClient.Delete(ctx, &pvcList.Items[i])
				}
			}()

			fake := newFakeADOClient(1)
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			pvcList := &corev1.PersistentVolumeClaimList{}
			Expect(k8sClient.List(ctx, pvcList,
				client.InNamespace(ns),
				client.MatchingLabels{labelPoolName: pool.Name})).To(Succeed())
			// MaxAgents=3, 1 template => 3 PVCs
			Expect(pvcList.Items).To(HaveLen(3))
		})
	})

	Context("Pod spec", func() {
		It("sets --once arg on agent container", func() {
			ensureSecret()
			pool := testPool("podspec-test", ns)
			pool.Spec.MaxAgents = 1
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, pool)
				podList := &corev1.PodList{}
				_ = k8sClient.List(ctx, podList,
					client.InNamespace(ns),
					client.MatchingLabels{labelPoolName: pool.Name})
				for i := range podList.Items {
					_ = k8sClient.Delete(ctx, &podList.Items[i])
				}
			}()

			fake := newFakeADOClient(1)
			fake.jobStatus.Pending = 1
			r := reconcilerWithFake(fake)

			_, _ = reconcile(r, pool.Name)
			_, _ = reconcile(r, pool.Name)
			_, err := reconcile(r, pool.Name)
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList,
				client.InNamespace(ns),
				client.MatchingLabels{labelPoolName: pool.Name})).To(Succeed())
			Expect(podList.Items).To(HaveLen(1))
			Expect(podList.Items[0].Spec.Containers[0].Args).To(ContainElement("--once"))
		})
	})
})

var _ = Describe("Helper functions", func() {
	Describe("clamp", func() {
		It("returns min when val < min", func() {
			Expect(clamp(0, 2, 10)).To(Equal(int32(2)))
		})
		It("returns max when val > max", func() {
			Expect(clamp(15, 0, 10)).To(Equal(int32(10)))
		})
		It("returns val when in range", func() {
			Expect(clamp(5, 0, 10)).To(Equal(int32(5)))
		})
	})

	Describe("randomString", func() {
		It("returns a string of the requested length", func() {
			for _, n := range []int{4, 6, 8} {
				s := randomString(n)
				Expect(s).To(HaveLen(n))
			}
		})
		It("produces different values on repeated calls", func() {
			seen := make(map[string]bool)
			for range 20 {
				seen[randomString(6)] = true
			}
			Expect(len(seen)).To(BeNumerically(">", 1))
		})
	})

	Describe("pvcSlotName", func() {
		It("generates a deterministic name", func() {
			Expect(pvcSlotName("mypool", "buildcache", 0)).To(Equal("mypool-cache-buildcache-00"))
			Expect(pvcSlotName("mypool", "buildcache", 10)).To(Equal("mypool-cache-buildcache-10"))
		})
		It("truncates long pool and cache names", func() {
			longPool := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 49 chars
			name := pvcSlotName(longPool, "buildcache", 0)
			Expect(len(name)).To(BeNumerically("<=", 253))
		})
	})
})

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
