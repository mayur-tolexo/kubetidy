package operator

import (
	"context"
	"errors"
	"sync"

	"github.com/kubetidy/kubetidy/internal/apis/usageprofile"
	"github.com/kubetidy/kubetidy/internal/model"
)

var errBoom = errors.New("boom")

// fakeLister is an in-test WorkloadLister.
type fakeLister struct {
	workloads []model.Workload
	err       error
}

func (f *fakeLister) List(_ context.Context) ([]model.Workload, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.workloads, nil
}

// fakeSampler returns a fixed per-workload sample set keyed by container name, optionally an
// error. errFor lets one specific workload (by Ref) error while others succeed.
type fakeSampler struct {
	samples map[string]Sample // by container name, returned for every workload
	err     error
	errFor  string // if set, only this workload Ref errors
	calls   int
}

func (f *fakeSampler) Sample(_ context.Context, w model.Workload) (map[string]Sample, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.errFor != "" && w.Ref() == f.errFor {
		return nil, errBoom
	}
	out := make(map[string]Sample, len(f.samples))
	for k, v := range f.samples {
		out[k] = v
	}
	return out, nil
}

// fakeStore is a map-backed Store keyed by namespace/name.
type fakeStore struct {
	mu       sync.Mutex
	profiles map[string]usageprofile.UsageProfile
	saves    int
	saveErr  error
	getErr   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{profiles: map[string]usageprofile.UsageProfile{}}
}

func storeKey(namespace, name string) string { return namespace + "/" + name }

func (f *fakeStore) Get(_ context.Context, namespace, name string) (usageprofile.UsageProfile, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return usageprofile.UsageProfile{}, false, f.getErr
	}
	p, ok := f.profiles[storeKey(namespace, name)]
	return p, ok, nil
}

func (f *fakeStore) Save(_ context.Context, profile usageprofile.UsageProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves++
	f.profiles[storeKey(profile.Namespace, profile.Name)] = profile
	return nil
}

func (f *fakeStore) get(namespace, name string) (usageprofile.UsageProfile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[storeKey(namespace, name)]
	return p, ok
}

func (f *fakeStore) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

// deployment is a small helper to build a single-container Deployment workload.
func deployment(namespace, name string, containers ...string) model.Workload {
	cs := make([]model.Container, 0, len(containers))
	for _, c := range containers {
		cs = append(cs, model.Container{Name: c})
	}
	return model.Workload{
		Kind:       model.KindDeployment,
		Name:       name,
		Namespace:  namespace,
		Containers: cs,
	}
}
