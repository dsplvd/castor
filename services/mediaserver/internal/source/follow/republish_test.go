package follow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// shape is a format whose inputs follow timeline, or are read directly when it is nil.
type shape struct{ timeline timeline.Source }

func (shape) Identity() source.Identity { return source.Identity{ContentType: media.HLS} }
func (shape) Recognize(string) bool     { return false }
func (shape) InputArgs(int) []string    { return nil }
func (s shape) Timeline(source.Client, media.Input, media.TrackKind) timeline.Source {
	return s.timeline
}
func (shape) Resolve(context.Context, source.Env, source.Subject) (source.Resolution, error) {
	return source.Resolution{}, nil
}

func republished(t *testing.T, s shape) *url.URL {
	t.Helper()
	p, err := media.NewProgram(media.Program{
		Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://cdn.example/live/index.m3u8"), ContentType: media.HLS}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	})
	if err != nil {
		t.Fatal(err)
	}
	followed, stop, err := New(&sourcetest.Document{}, source.Formats{s}, nil).Republish(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop() })
	in, _ := followed.PrimaryInput()
	return in.URL
}

func TestAnInputWithNoTimelineIsReadDirectly(t *testing.T) {
	if got := republished(t, shape{}).String(); got != "https://cdn.example/live/index.m3u8" {
		t.Errorf("an input with no timeline reads %s, want the origin's own", got)
	}
}

func TestATimelineCastorCannotStartStaysFfmpegsToRead(t *testing.T) {
	refused := &scripted{replies: []reply{{err: &timeline.Failure{Status: http.StatusForbidden, Err: errors.New("expired")}}}}
	if got := republished(t, shape{timeline: refused}).String(); got != "https://cdn.example/live/index.m3u8" {
		t.Errorf("an unreadable timeline reads %s, want the origin's own", got)
	}
}

type closingOrigin struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (o *closingOrigin) Window(context.Context) (timeline.Window, error) {
	return timeline.Window{Segments: []timeline.Segment{{URI: "https://origin.example/0.ts", Duration: time.Second}}, Closed: true}, nil
}
func (o *closingOrigin) Read(ctx context.Context, _ string, _ timeline.Range) (io.ReadCloser, error) {
	close(o.started)
	<-ctx.Done()
	close(o.cancelled)
	<-o.release
	return nil, context.Cause(ctx)
}

func TestRepublisherStopJoinsOriginCleanup(t *testing.T) {
	origin := &closingOrigin{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	program, err := media.NewProgram(media.Program{
		Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://origin.example/index.m3u8"), ContentType: media.HLS}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
		ClockInput: media.PrimaryInputID, EndPolicy: media.EndAtLongest,
	})
	if err != nil {
		t.Fatal(err)
	}
	followed, stop, err := New(&sourcetest.Document{}, source.Formats{shape{timeline: origin}}, nil).Republish(t.Context(), program)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() { close(origin.release) })
	t.Cleanup(func() { release(); _ = stop() })
	input, _ := followed.PrimaryInput()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get(input.URL.ResolveReference(&url.URL{Path: "primary/0"}).String())
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	<-origin.started
	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	<-origin.cancelled
	select {
	case err := <-stopped:
		t.Fatalf("stop returned before origin cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not join completed origin cleanup")
	}
	<-requestDone
}

type closingWindow struct{ closingOrigin }

func (o *closingWindow) Window(ctx context.Context) (timeline.Window, error) {
	close(o.started)
	<-ctx.Done()
	close(o.cancelled)
	<-o.release
	return timeline.Window{}, context.Cause(ctx)
}
func TestServerCloseJoinsPlaylistHandler(t *testing.T) {
	origin := &closingWindow{closingOrigin{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}}
	server := serving(t, origin, nil)
	release := sync.OnceFunc(func() { close(origin.release) })
	t.Cleanup(release)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get(server.URL("primary").String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-origin.started
	stopped := make(chan error, 1)
	go func() { stopped <- server.Close() }()
	<-origin.cancelled
	select {
	case err := <-stopped:
		t.Fatalf("server returned before handler cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not join completed handler")
	}
	<-requestDone
}
