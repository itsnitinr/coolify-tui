package coolify

import (
	"context"
	"net/http"
	"testing"
)

func TestDecodeCollectionAcceptsPlainArray(t *testing.T) {
	got, err := decodeCollection[Deployment](
		[]byte(`[{"deployment_uuid":"d1"},{"deployment_uuid":"d2"}]`), "deployments")
	if err != nil {
		t.Fatalf("decodeCollection: %v", err)
	}
	if len(got) != 2 || got[0].DeploymentUUID != "d1" || got[1].DeploymentUUID != "d2" {
		t.Errorf("got %+v, want d1 then d2", got)
	}
}

func TestDecodeCollectionUnwrapsPaginatedEnvelope(t *testing.T) {
	// What GET /deployments/applications/{uuid} actually returns, despite the
	// OpenAPI spec documenting a bare array.
	got, err := decodeCollection[Deployment](
		[]byte(`{"count":2,"deployments":[{"deployment_uuid":"d1"},{"deployment_uuid":"d2"}]}`),
		"deployments")
	if err != nil {
		t.Fatalf("decodeCollection: %v", err)
	}
	if len(got) != 2 || got[0].DeploymentUUID != "d1" {
		t.Errorf("got %+v, want the two deployments from the envelope", got)
	}
}

func TestDecodeCollectionRecoversKeyIndexedObject(t *testing.T) {
	// Laravel's sortBy preserves array keys, so PHP json_encodes the collection
	// as an object once the keys are no longer a sequential run. The numeric keys
	// carry the intended order.
	got, err := decodeCollection[Deployment](
		[]byte(`{"2":{"deployment_uuid":"third"},"0":{"deployment_uuid":"first"},"1":{"deployment_uuid":"second"}}`),
		"deployments")
	if err != nil {
		t.Fatalf("decodeCollection: %v", err)
	}
	want := []string{"first", "second", "third"}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	for i, w := range want {
		if got[i].DeploymentUUID != w {
			t.Errorf("index %d = %q, want %q (numeric key order)", i, got[i].DeploymentUUID, w)
		}
	}
}

func TestDecodeCollectionKeyedInsideEnvelope(t *testing.T) {
	// Both quirks at once: an envelope whose inner collection was also sorted.
	got, err := decodeCollection[Deployment](
		[]byte(`{"count":2,"deployments":{"1":{"deployment_uuid":"b"},"0":{"deployment_uuid":"a"}}}`),
		"deployments")
	if err != nil {
		t.Fatalf("decodeCollection: %v", err)
	}
	if len(got) != 2 || got[0].DeploymentUUID != "a" || got[1].DeploymentUUID != "b" {
		t.Errorf("got %+v, want a then b", got)
	}
}

func TestDecodeCollectionEmptyShapes(t *testing.T) {
	for _, body := range []string{`[]`, `{}`, `null`, ``, `{"count":0,"deployments":[]}`} {
		got, err := decodeCollection[Deployment]([]byte(body), "deployments")
		if err != nil {
			t.Errorf("decodeCollection(%q): %v", body, err)
		}
		if len(got) != 0 {
			t.Errorf("decodeCollection(%q) = %+v, want empty", body, got)
		}
	}
}

func TestDecodeCollectionRejectsScalar(t *testing.T) {
	if _, err := decodeCollection[Deployment]([]byte(`"nope"`), "deployments"); err == nil {
		t.Error("a scalar body should be an error, not silently empty")
	}
}

func TestApplicationDeploymentsHandlesEnvelope(t *testing.T) {
	// End-to-end: the shape that produced "cannot unmarshal object into Go value
	// of type []coolify.Deployment" against a real instance.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/deployments/applications/y804cgkw484k88sgo88ow8ok" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"deployments":[
			{"deployment_uuid":"dep-1","status":"finished","commit":"9f2c1ab77e3d4"}]}`))
	})

	got, err := c.ApplicationDeployments(context.Background(), "y804cgkw484k88sgo88ow8ok", 0, 25)
	if err != nil {
		t.Fatalf("ApplicationDeployments: %v", err)
	}
	if len(got) != 1 || got[0].DeploymentUUID != "dep-1" {
		t.Errorf("got %+v, want one deployment", got)
	}
}

func TestDeploymentsHandlesKeyedObject(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"1":{"deployment_uuid":"b","status":"in_progress"},
			"0":{"deployment_uuid":"a","status":"queued"}}`))
	})
	got, err := c.Deployments(context.Background())
	if err != nil {
		t.Fatalf("Deployments: %v", err)
	}
	if len(got) != 2 || got[0].DeploymentUUID != "a" {
		t.Errorf("got %+v, want a then b", got)
	}
}
