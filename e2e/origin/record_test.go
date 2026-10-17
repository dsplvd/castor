package origin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestsRecordArrivalBeforeCompletion(t *testing.T) {
	o := &Origin{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := o.record(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			close(entered)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/first", nil))
	}()
	<-entered
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/second", nil))
	rows := o.Requests()
	if rows[0].Path != "/first" || rows[0].Status != 0 || rows[1].Status != http.StatusNoContent {
		t.Fatalf("trace=%v", rows)
	}
	close(release)
	<-done
	if o.Requests()[0].Completed.IsZero() {
		t.Fatal("completion missing")
	}
}
