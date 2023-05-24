package servemux

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type route struct {
	method string
	path   string
}

func (r route) pattern() string {
	if r.method != "" {
		return r.method + " " + r.path
	}
	return r.path
}

var (
	staticRoutes = []*route{
		{"GET", "/{$}"},
		{"GET", "/Makefile"},
		{"GET", "/articles/"},
		{"GET", "/articles/go_command.html"},
		{"GET", "/articles/index.html"},
		{"GET", "/articles/wiki/"},
		{"GET", "/articles/wiki/Makefile"},
		{"GET", "/articles/wiki/edit.html"},
		{"GET", "/articles/wiki/final-noclosure.go"},
		{"GET", "/articles/wiki/final-noerror.go"},
		{"GET", "/articles/wiki/final-parsetemplate.go"},
		{"GET", "/articles/wiki/final-template.go"},
		{"GET", "/articles/wiki/final.go"},
		{"GET", "/articles/wiki/get.go"},
		{"GET", "/articles/wiki/http-sample.go"},
		{"GET", "/articles/wiki/index.html"},
		{"GET", "/articles/wiki/notemplate.go"},
		{"GET", "/articles/wiki/part1-noerror.go"},
		{"GET", "/articles/wiki/part1.go"},
		{"GET", "/articles/wiki/part2.go"},
		{"GET", "/articles/wiki/part3-errorhandling.go"},
		{"GET", "/articles/wiki/part3.go"},
		{"GET", "/articles/wiki/test.bash"},
		{"GET", "/articles/wiki/test_Test.txt.good"},
		{"GET", "/articles/wiki/test_edit.good"},
		{"GET", "/articles/wiki/test_view.good"},
		{"GET", "/articles/wiki/view.html"},
		{"GET", "/cmd.html"},
		{"GET", "/code.html"},
		{"GET", "/codewalk/"},
		{"GET", "/codewalk/codewalk.css"},
		{"GET", "/codewalk/codewalk.js"},
		{"GET", "/codewalk/codewalk.xml"},
		{"GET", "/codewalk/functions.xml"},
		{"GET", "/codewalk/markov.go"},
		{"GET", "/codewalk/markov.xml"},
		{"GET", "/codewalk/pig.go"},
		{"GET", "/codewalk/popout.png"},
		{"GET", "/codewalk/run"},
		{"GET", "/codewalk/sharemem.xml"},
		{"GET", "/codewalk/urlpoll.go"},
		{"GET", "/contrib.html"},
		{"GET", "/contribute.html"},
		{"GET", "/debugging_with_gdb.html"},
		{"GET", "/devel/"},
		{"GET", "/devel/release.html"},
		{"GET", "/devel/weekly.html"},
		{"GET", "/docs.html"},
		{"GET", "/effective_go.html"},
		{"GET", "/files.log"},
		{"GET", "/gccgo_contribute.html"},
		{"GET", "/gccgo_install.html"},
		{"GET", "/go-logo-black.png"},
		{"GET", "/go-logo-blue.png"},
		{"GET", "/go-logo-white.png"},
		{"GET", "/go1.1.html"},
		{"GET", "/go1.2.html"},
		{"GET", "/go1.html"},
		{"GET", "/go1compat.html"},
		{"GET", "/go_faq.html"},
		{"GET", "/go_mem.html"},
		{"GET", "/go_spec.html"},
		{"GET", "/gopher/"},
		{"GET", "/gopher/appenginegopher.jpg"},
		{"GET", "/gopher/appenginegophercolor.jpg"},
		{"GET", "/gopher/appenginelogo.gif"},
		{"GET", "/gopher/bumper.png"},
		{"GET", "/gopher/bumper192x108.png"},
		{"GET", "/gopher/bumper320x180.png"},
		{"GET", "/gopher/bumper480x270.png"},
		{"GET", "/gopher/bumper640x360.png"},
		{"GET", "/gopher/doc.png"},
		{"GET", "/gopher/frontpage.png"},
		{"GET", "/gopher/gopherbw.png"},
		{"GET", "/gopher/gophercolor.png"},
		{"GET", "/gopher/gophercolor16x16.png"},
		{"GET", "/gopher/help.png"},
		{"GET", "/gopher/pencil/"},
		{"GET", "/gopher/pencil/gopherhat.jpg"},
		{"GET", "/gopher/pencil/gopherhelmet.jpg"},
		{"GET", "/gopher/pencil/gophermega.jpg"},
		{"GET", "/gopher/pencil/gopherrunning.jpg"},
		{"GET", "/gopher/pencil/gopherswim.jpg"},
		{"GET", "/gopher/pencil/gopherswrench.jpg"},
		{"GET", "/gopher/pkg.png"},
		{"GET", "/gopher/project.png"},
		{"GET", "/gopher/ref.png"},
		{"GET", "/gopher/run.png"},
		{"GET", "/gopher/talks.png"},
		{"GET", "/help.html"},
		{"GET", "/ie.css"},
		{"GET", "/install-source.html"},
		{"GET", "/install.html"},
		{"GET", "/logo-153x55.png"},
		{"GET", "/play/"},
		{"GET", "/play/fib.go"},
		{"GET", "/play/hello.go"},
		{"GET", "/play/life.go"},
		{"GET", "/play/peano.go"},
		{"GET", "/play/pi.go"},
		{"GET", "/play/sieve.go"},
		{"GET", "/play/solitaire.go"},
		{"GET", "/play/tree.go"},
		{"GET", "/progs/"},
		{"GET", "/progs/cgo1.go"},
		{"GET", "/progs/cgo2.go"},
		{"GET", "/progs/cgo3.go"},
		{"GET", "/progs/cgo4.go"},
		{"GET", "/progs/defer.go"},
		{"GET", "/progs/defer.out"},
		{"GET", "/progs/defer2.go"},
		{"GET", "/progs/defer2.out"},
		{"GET", "/progs/eff_bytesize.go"},
		{"GET", "/progs/eff_bytesize.out"},
		{"GET", "/progs/eff_qr.go"},
		{"GET", "/progs/eff_sequence.go"},
		{"GET", "/progs/eff_sequence.out"},
		{"GET", "/progs/eff_unused1.go"},
		{"GET", "/progs/eff_unused2.go"},
		{"GET", "/progs/error.go"},
		{"GET", "/progs/error2.go"},
		{"GET", "/progs/error3.go"},
		{"GET", "/progs/error4.go"},
		{"GET", "/progs/go1.go"},
		{"GET", "/progs/gobs1.go"},
		{"GET", "/progs/gobs2.go"},
		{"GET", "/progs/image_draw.go"},
		{"GET", "/progs/image_package1.go"},
		{"GET", "/progs/image_package1.out"},
		{"GET", "/progs/image_package2.go"},
		{"GET", "/progs/image_package2.out"},
		{"GET", "/progs/image_package3.go"},
		{"GET", "/progs/image_package3.out"},
		{"GET", "/progs/image_package4.go"},
		{"GET", "/progs/image_package4.out"},
		{"GET", "/progs/image_package5.go"},
		{"GET", "/progs/image_package5.out"},
		{"GET", "/progs/image_package6.go"},
		{"GET", "/progs/image_package6.out"},
		{"GET", "/progs/interface.go"},
		{"GET", "/progs/interface2.go"},
		{"GET", "/progs/interface2.out"},
		{"GET", "/progs/json1.go"},
		{"GET", "/progs/json2.go"},
		{"GET", "/progs/json2.out"},
		{"GET", "/progs/json3.go"},
		{"GET", "/progs/json4.go"},
		{"GET", "/progs/json5.go"},
		{"GET", "/progs/run"},
		{"GET", "/progs/slices.go"},
		{"GET", "/progs/timeout1.go"},
		{"GET", "/progs/timeout2.go"},
		{"GET", "/progs/update.bash"},
		{"GET", "/root.html"},
		{"GET", "/share.png"},
		{"GET", "/sieve.gif"},
		{"GET", "/tos.html"},
	}

	githubAPIRoutes = []*route{
		{"DELETE", "/applications/{client_id}/tokens"},
		{"DELETE", "/applications/{client_id}/tokens/{access_token}"},
		{"DELETE", "/authorizations/{id}"},
		{"DELETE", "/gists/{id}"},
		{"DELETE", "/gists/{id}/star"},
		{"DELETE", "/notifications/threads/{id}/subscription"},
		{"DELETE", "/orgs/{org}/members/{user}"},
		{"DELETE", "/orgs/{org}/public_members/{user}"},
		{"DELETE", "/repos/{owner}/{repo}"},
		{"DELETE", "/repos/{owner}/{repo}/collaborators/{user}"},
		{"DELETE", "/repos/{owner}/{repo}/comments/{id}"},
		{"DELETE", "/repos/{owner}/{repo}/downloads/{id}"},
		{"DELETE", "/repos/{owner}/{repo}/hooks/{id}"},
		{"DELETE", "/repos/{owner}/{repo}/issues/{number}/labels"},
		{"DELETE", "/repos/{owner}/{repo}/issues/{number}/labels/{name}"},
		{"DELETE", "/repos/{owner}/{repo}/issues/comments/{id}"},
		{"DELETE", "/repos/{owner}/{repo}/keys/{id}"},
		{"DELETE", "/repos/{owner}/{repo}/labels/{name}"},
		{"DELETE", "/repos/{owner}/{repo}/milestones/{number}"},
		{"DELETE", "/repos/{owner}/{repo}/pulls/comments/{number}"},
		{"DELETE", "/repos/{owner}/{repo}/releases/{id}"},
		{"DELETE", "/repos/{owner}/{repo}/subscription"},
		{"DELETE", "/teams/{id}"},
		{"DELETE", "/teams/{id}/members/{user}"},
		{"DELETE", "/teams/{id}/repos/{owner}/{repo}"},
		{"DELETE", "/user/emails"},
		{"DELETE", "/user/following/{user}"},
		{"DELETE", "/user/keys/{id}"},
		{"DELETE", "/user/starred/{owner}/{repo}"},
		{"DELETE", "/user/subscriptions/{owner}/{repo}"},
		{"GET", "/applications/{client_id}/tokens/{access_token}"},
		{"GET", "/authorizations"},
		{"GET", "/authorizations/{id}"},
		{"GET", "/emojis"},
		{"GET", "/events"},
		{"GET", "/feeds"},
		{"GET", "/gists"},
		{"GET", "/gists/{id}"},
		{"GET", "/gists/{id}/star"},
		{"GET", "/gists/public"},
		{"GET", "/gists/starred"},
		{"GET", "/gitignore/templates"},
		{"GET", "/gitignore/templates/{name}"},
		{"GET", "/issues"},
		{"GET", "/legacy/issues/search/{owner}/{repo}/{state}/{keyword}"},
		{"GET", "/legacy/repos/search/{keyword}"},
		{"GET", "/legacy/user/email/{email}"},
		{"GET", "/legacy/user/search/{keyword}"},
		{"GET", "/meta"},
		{"GET", "/networks/{owner}/{repo}/events"},
		{"GET", "/notifications"},
		{"GET", "/notifications/threads/{id}"},
		{"GET", "/notifications/threads/{id}/subscription"},
		{"GET", "/orgs/{org}"},
		{"GET", "/orgs/{org}/events"},
		{"GET", "/orgs/{org}/issues"},
		{"GET", "/orgs/{org}/members"},
		{"GET", "/orgs/{org}/members/{user}"},
		{"GET", "/orgs/{org}/public_members"},
		{"GET", "/orgs/{org}/public_members/{user}"},
		{"GET", "/orgs/{org}/repos"},
		{"GET", "/orgs/{org}/teams"},
		{"GET", "/rate_limit"},
		{"GET", "/repos/{owner}/{repo}"},
		{"GET", "/repos/{owner}/{repo}/{archive_format}/{ref}"},
		{"GET", "/repos/{owner}/{repo}/assignees"},
		{"GET", "/repos/{owner}/{repo}/assignees/{assignee}"},
		{"GET", "/repos/{owner}/{repo}/branches"},
		{"GET", "/repos/{owner}/{repo}/branches/{branch}"},
		{"GET", "/repos/{owner}/{repo}/collaborators"},
		{"GET", "/repos/{owner}/{repo}/collaborators/{user}"},
		{"GET", "/repos/{owner}/{repo}/comments"},
		{"GET", "/repos/{owner}/{repo}/comments/{id}"},
		{"GET", "/repos/{owner}/{repo}/commits"},
		{"GET", "/repos/{owner}/{repo}/commits/{sha}"},
		{"GET", "/repos/{owner}/{repo}/commits/{sha}/comments"},
		{"GET", "/repos/{owner}/{repo}/contributors"},
		{"GET", "/repos/{owner}/{repo}/downloads"},
		{"GET", "/repos/{owner}/{repo}/downloads/{id}"},
		{"GET", "/repos/{owner}/{repo}/events"},
		{"GET", "/repos/{owner}/{repo}/forks"},
		{"GET", "/repos/{owner}/{repo}/git/blobs/{sha}"},
		{"GET", "/repos/{owner}/{repo}/git/commits/{sha}"},
		{"GET", "/repos/{owner}/{repo}/git/refs"},
		{"GET", "/repos/{owner}/{repo}/git/tags/{sha}"},
		{"GET", "/repos/{owner}/{repo}/git/trees/{sha}"},
		{"GET", "/repos/{owner}/{repo}/hooks"},
		{"GET", "/repos/{owner}/{repo}/hooks/{id}"},
		{"GET", "/repos/{owner}/{repo}/issues"},
		{"GET", "/repos/{owner}/{repo}/issues/{number}"},
		{"GET", "/repos/{owner}/{repo}/issues/{number}/comments"},
		{"GET", "/repos/{owner}/{repo}/issues/{number}/events"},
		{"GET", "/repos/{owner}/{repo}/issues/{number}/labels"},
		{"GET", "/repos/{owner}/{repo}/issues/comments"},
		{"GET", "/repos/{owner}/{repo}/issues/comments/{id}"},
		{"GET", "/repos/{owner}/{repo}/issues/events"},
		{"GET", "/repos/{owner}/{repo}/issues/events/{id}"},
		{"GET", "/repos/{owner}/{repo}/keys"},
		{"GET", "/repos/{owner}/{repo}/keys/{id}"},
		{"GET", "/repos/{owner}/{repo}/labels"},
		{"GET", "/repos/{owner}/{repo}/labels/{name}"},
		{"GET", "/repos/{owner}/{repo}/languages"},
		{"GET", "/repos/{owner}/{repo}/milestones"},
		{"GET", "/repos/{owner}/{repo}/milestones/{number}"},
		{"GET", "/repos/{owner}/{repo}/milestones/{number}/labels"},
		{"GET", "/repos/{owner}/{repo}/notifications"},
		{"GET", "/repos/{owner}/{repo}/pulls"},
		{"GET", "/repos/{owner}/{repo}/pulls/{number}"},
		{"GET", "/repos/{owner}/{repo}/pulls/{number}/comments"},
		{"GET", "/repos/{owner}/{repo}/pulls/{number}/commits"},
		{"GET", "/repos/{owner}/{repo}/pulls/{number}/files"},
		{"GET", "/repos/{owner}/{repo}/pulls/{number}/merge"},
		{"GET", "/repos/{owner}/{repo}/pulls/comments"},
		{"GET", "/repos/{owner}/{repo}/pulls/comments/{number}"},
		{"GET", "/repos/{owner}/{repo}/readme"},
		{"GET", "/repos/{owner}/{repo}/releases"},
		{"GET", "/repos/{owner}/{repo}/releases/{id}"},
		{"GET", "/repos/{owner}/{repo}/releases/{id}/assets"},
		{"GET", "/repos/{owner}/{repo}/stargazers"},
		{"GET", "/repos/{owner}/{repo}/stats/code_frequency"},
		{"GET", "/repos/{owner}/{repo}/stats/commit_activity"},
		{"GET", "/repos/{owner}/{repo}/stats/contributors"},
		{"GET", "/repos/{owner}/{repo}/stats/participation"},
		{"GET", "/repos/{owner}/{repo}/stats/punch_card"},
		{"GET", "/repos/{owner}/{repo}/statuses/{ref}"},
		{"GET", "/repos/{owner}/{repo}/subscribers"},
		{"GET", "/repos/{owner}/{repo}/subscription"},
		{"GET", "/repos/{owner}/{repo}/tags"},
		{"GET", "/repos/{owner}/{repo}/teams"},
		{"GET", "/repositories"},
		{"GET", "/search/code"},
		{"GET", "/search/issues"},
		{"GET", "/search/repositories"},
		{"GET", "/search/users"},
		{"GET", "/teams/{id}"},
		{"GET", "/teams/{id}/members"},
		{"GET", "/teams/{id}/members/{user}"},
		{"GET", "/teams/{id}/repos"},
		{"GET", "/teams/{id}/repos/{owner}/{repo}"},
		{"GET", "/user"},
		{"GET", "/user/emails"},
		{"GET", "/user/followers"},
		{"GET", "/user/following"},
		{"GET", "/user/following/{user}"},
		{"GET", "/user/issues"},
		{"GET", "/user/keys"},
		{"GET", "/user/keys/{id}"},
		{"GET", "/user/orgs"},
		{"GET", "/user/repos"},
		{"GET", "/user/starred"},
		{"GET", "/user/starred/{owner}/{repo}"},
		{"GET", "/user/subscriptions"},
		{"GET", "/user/subscriptions/{owner}/{repo}"},
		{"GET", "/user/teams"},
		{"GET", "/users"},
		{"GET", "/users/{user}"},
		{"GET", "/users/{user}/events"},
		{"GET", "/users/{user}/events/orgs/{org}"},
		{"GET", "/users/{user}/events/public"},
		{"GET", "/users/{user}/followers"},
		{"GET", "/users/{user}/following"},
		{"GET", "/users/{user}/following/{target_user}"},
		{"GET", "/users/{user}/gists"},
		{"GET", "/users/{user}/keys"},
		{"GET", "/users/{user}/orgs"},
		{"GET", "/users/{user}/received_events"},
		{"GET", "/users/{user}/received_events/public"},
		{"GET", "/users/{user}/repos"},
		{"GET", "/users/{user}/starred"},
		{"GET", "/users/{user}/subscriptions"},
		{"PATCH", "/authorizations/{id}"},
		{"PATCH", "/gists/{id}"},
		{"PATCH", "/notifications/threads/{id}"},
		{"PATCH", "/orgs/{org}"},
		{"PATCH", "/repos/{owner}/{repo}"},
		{"PATCH", "/repos/{owner}/{repo}/comments/{id}"},
		{"PATCH", "/repos/{owner}/{repo}/hooks/{id}"},
		{"PATCH", "/repos/{owner}/{repo}/issues/{number}"},
		{"PATCH", "/repos/{owner}/{repo}/issues/comments/{id}"},
		{"PATCH", "/repos/{owner}/{repo}/keys/{id}"},
		{"PATCH", "/repos/{owner}/{repo}/labels/{name}"},
		{"PATCH", "/repos/{owner}/{repo}/milestones/{number}"},
		{"PATCH", "/repos/{owner}/{repo}/pulls/{number}"},
		{"PATCH", "/repos/{owner}/{repo}/pulls/comments/{number}"},
		{"PATCH", "/repos/{owner}/{repo}/releases/{id}"},
		{"PATCH", "/teams/{id}"},
		{"PATCH", "/user"},
		{"PATCH", "/user/keys/{id}"},
		{"POST", "/authorizations"},
		{"POST", "/gists"},
		{"POST", "/gists/{id}/forks"},
		{"POST", "/markdown"},
		{"POST", "/markdown/raw"},
		{"POST", "/orgs/{org}/repos"},
		{"POST", "/orgs/{org}/teams"},
		{"POST", "/repos/{owner}/{repo}/commits/{sha}/comments"},
		{"POST", "/repos/{owner}/{repo}/forks"},
		{"POST", "/repos/{owner}/{repo}/git/blobs"},
		{"POST", "/repos/{owner}/{repo}/git/commits"},
		{"POST", "/repos/{owner}/{repo}/git/refs"},
		{"POST", "/repos/{owner}/{repo}/git/tags"},
		{"POST", "/repos/{owner}/{repo}/git/trees"},
		{"POST", "/repos/{owner}/{repo}/hooks"},
		{"POST", "/repos/{owner}/{repo}/hooks/{id}/tests"},
		{"POST", "/repos/{owner}/{repo}/issues"},
		{"POST", "/repos/{owner}/{repo}/issues/{number}/comments"},
		{"POST", "/repos/{owner}/{repo}/issues/{number}/labels"},
		{"POST", "/repos/{owner}/{repo}/keys"},
		{"POST", "/repos/{owner}/{repo}/labels"},
		{"POST", "/repos/{owner}/{repo}/merges"},
		{"POST", "/repos/{owner}/{repo}/milestones"},
		{"POST", "/repos/{owner}/{repo}/pulls"},
		{"POST", "/repos/{owner}/{repo}/releases"},
		{"POST", "/repos/{owner}/{repo}/statuses/{ref}"},
		{"POST", "/user/emails"},
		{"POST", "/user/keys"},
		{"POST", "/user/repos"},
		{"PUT", "/authorizations/clients/{client_id}"},
		{"PUT", "/gists/{id}/star"},
		{"PUT", "/notifications"},
		{"PUT", "/notifications/threads/{id}/subscription"},
		{"PUT", "/orgs/{org}/public_members/{user}"},
		{"PUT", "/repos/{owner}/{repo}/collaborators/{user}"},
		{"PUT", "/repos/{owner}/{repo}/issues/{number}/labels"},
		{"PUT", "/repos/{owner}/{repo}/notifications"},
		{"PUT", "/repos/{owner}/{repo}/pulls/{number}/comments"},
		{"PUT", "/repos/{owner}/{repo}/pulls/{number}/merge"},
		{"PUT", "/repos/{owner}/{repo}/subscription"},
		{"PUT", "/teams/{id}/members/{user}"},
		{"PUT", "/teams/{id}/repos/{owner}/{repo}"},
		{"PUT", "/user/following/{user}"},
		{"PUT", "/user/starred/{owner}/{repo}"},
		{"PUT", "/user/subscriptions/{owner}/{repo}"},
	}

	gplusAPIRoutes = []*route{
		{"DELETE", "/moments/{id}"},
		{"GET", "/activities"},
		{"GET", "/activities/{activityId}"},
		{"GET", "/activities/{activityId}/comments"},
		{"GET", "/activities/{activityId}/people/{collection}"},
		{"GET", "/comments/{commentId}"},
		{"GET", "/people"},
		{"GET", "/people/{userId}"},
		{"GET", "/people/{userId}/activities/{collection}"},
		{"GET", "/people/{userId}/moments/{collection}"},
		{"GET", "/people/{userId}/openIdConnect"},
		{"GET", "/people/{userId}/people/{collection}"},
		{"POST", "/people/{userId}/moments/{collection}"},
	}

	parseAPIRoutes = []*route{
		{"DELETE", "/1/classes/{className}/{objectId}"},
		{"DELETE", "/1/installations/{objectId}"},
		{"DELETE", "/1/roles/{objectId}"},
		{"DELETE", "/1/users/{objectId}"},
		{"GET", "/1/classes/{className}"},
		{"GET", "/1/classes/{className}/{objectId}"},
		{"GET", "/1/installations"},
		{"GET", "/1/installations/{objectId}"},
		{"GET", "/1/login"},
		{"GET", "/1/roles"},
		{"GET", "/1/roles/{objectId}"},
		{"GET", "/1/users"},
		{"GET", "/1/users/{objectId}"},
		{"POST", "/1/classes/{className}"},
		{"POST", "/1/events/{eventName}"},
		{"POST", "/1/files/{fileName}"},
		{"POST", "/1/functions"},
		{"POST", "/1/installations"},
		{"POST", "/1/push"},
		{"POST", "/1/requestPasswordReset"},
		{"POST", "/1/roles"},
		{"POST", "/1/users"},
		{"PUT", "/1/classes/{className}/{objectId}"},
		{"PUT", "/1/installations/{objectId}"},
		{"PUT", "/1/roles/{objectId}"},
		{"PUT", "/1/users/{objectId}"},
	}

	connectRoutes = []*route{
		{"CONNECT", "example.com/foo/bar/"},
		{"CONNECT", "example.com/foo/bar"},
		{"CONNECT", "example.com/foobar/"},
		{"CONNECT", "example.com/foobar"},
		{"CONNECT", "example.com"},
		{"CONNECT", "example.org/"},
		{"CONNECT", "example.org/foobar"},
		{"CONNECT", "example.org/foobar/"},
		{"CONNECT", "example.org/foo/bar"},
		{"CONNECT", "example.org/foo/bar/"},
		{"CONNECT", "example.net:8080"},
		{"CONNECT", "example.net:8080/foobar"},
		{"CONNECT", "example.net:8080/foobar/"},
		{"CONNECT", "example.net:8080/foo/bar"},
		{"CONNECT", "example.net:8080/foo/bar/"},
	}
)

func TestServeMux(t *testing.T) {
	mux := NewServeMux()
	routesGroup := [][]*route{staticRoutes, githubAPIRoutes, gplusAPIRoutes, parseAPIRoutes, connectRoutes}
	for _, routes := range routesGroup {
		for _, route := range routes {
			pattern := route.pattern()
			mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(pattern))
			})
		}
	}
	for _, routes := range routesGroup {
		for _, route := range routes {
			req := httptest.NewRequest(route.method, strings.TrimSuffix(route.path, "{$}"), nil)
			rec := httptest.NewRecorder()
			h, pattern := mux.Handler(req)
			if got, want := pattern, route.pattern(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
			h.ServeHTTP(rec, req)
			if got, want := rec.Body.String(), route.pattern(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
			rec = httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if got, want := rec.Body.String(), route.pattern(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		}
	}
}

func setParallel(t *testing.T) {
	if testing.Short() {
		t.Parallel()
	}
}

type testMode string

const (
	http1Mode  = testMode("h1")     // HTTP/1.1
	https1Mode = testMode("https1") // HTTPS/1.1
	http2Mode  = testMode("h2")     // HTTP/2
)

type testNotParallelOpt struct{}

var (
	testNotParallel = testNotParallelOpt{}
)

type TBRun[T any] interface {
	testing.TB
	Run(string, func(T)) bool
}

// run runs a client/server test in a variety of test configurations.
//
// Tests execute in HTTP/1.1 and HTTP/2 modes by default.
// To run in a different set of configurations, pass a []testMode option.
//
// Tests call t.Parallel() by default.
// To disable parallel execution, pass the testNotParallel option.
func run[T TBRun[T]](t T, f func(t T, mode testMode), opts ...any) {
	t.Helper()
	modes := []testMode{http1Mode, http2Mode}
	parallel := true
	for _, opt := range opts {
		switch opt := opt.(type) {
		case []testMode:
			modes = opt
		case testNotParallelOpt:
			parallel = false
		default:
			t.Fatalf("unknown option type %T", opt)
		}
	}
	if t, ok := any(t).(*testing.T); ok && parallel {
		setParallel(t)
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t T) {
			t.Helper()
			if t, ok := any(t).(*testing.T); ok && parallel {
				setParallel(t)
			}
			t.Cleanup(func() {
				afterTest(t)
			})
			f(t, mode)
		})
	}
}

var leakReported bool

func afterTest(t testing.TB) {
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	if testing.Short() {
		return
	}
	if leakReported {
		// To avoid confusion, only report the first leak of each test run.
		// After the first leak has been reported, we can't tell whether the leaked
		// goroutines are a new leak from a subsequent test or just the same
		// goroutines from the first leak still hanging around, and we may add a lot
		// of latency waiting for them to exit at the end of each test.
		return
	}

	// We shouldn't be running the leak check for parallel tests, because we might
	// report the goroutines from a test that is still running as a leak from a
	// completely separate test that has just finished. So we use non-atomic loads
	// and stores for the leakReported variable, and store every time we start a
	// leak check so that the race detector will flag concurrent leak checks as a
	// race even if we don't detect any leaks.
	leakReported = true

	var bad string
	badSubstring := map[string]string{
		").readLoop(":  "a Transport",
		").writeLoop(": "a Transport",
		"created by net/http/httptest.(*Server).Start": "an httptest.Server",
		"timeoutHandler":        "a TimeoutHandler",
		"net.(*netFD).connect(": "a timing out dial",
		").noteClientGone(":     "a closenotifier sender",
	}
	var stacks string
	for i := 0; i < 10; i++ {
		bad = ""
		stacks = strings.Join(interestingGoroutines(), "\n\n")
		for substr, what := range badSubstring {
			if strings.Contains(stacks, substr) {
				bad = what
			}
		}
		if bad == "" {
			leakReported = false
			return
		}
		// Bad stuff found, but goroutines might just still be
		// shutting down, so give it some time.
		time.Sleep(250 * time.Millisecond)
	}
	t.Errorf("Test appears to have leaked %s:\n%s", bad, stacks)
}

func interestingGoroutines() (gs []string) {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		_, stack, _ := strings.Cut(g, "\n")
		stack = strings.TrimSpace(stack)
		if stack == "" ||
			strings.Contains(stack, "testing.(*M).before.func1") ||
			strings.Contains(stack, "os/signal.signal_recv") ||
			strings.Contains(stack, "created by net.startServer") ||
			strings.Contains(stack, "created by testing.RunTests") ||
			strings.Contains(stack, "closeWriteAndWait") ||
			strings.Contains(stack, "testing.Main(") ||
			// These only show up with GOTRACEBACK=2; Issue 5005 (comment 28)
			strings.Contains(stack, "runtime.goexit") ||
			strings.Contains(stack, "created by runtime.gc") ||
			strings.Contains(stack, "interestingGoroutines") ||
			strings.Contains(stack, "runtime.MHeap_Scavenger") {
			continue
		}
		gs = append(gs, stack)
	}
	sort.Strings(gs)
	return
}

type clientServerTest struct {
	t  testing.TB
	h2 bool
	h  http.Handler
	ts *httptest.Server
	tr *http.Transport
	c  *http.Client
}

func (t *clientServerTest) close() {
	t.tr.CloseIdleConnections()
	t.ts.Close()
}

func (t *clientServerTest) getURL(u string) string {
	res, err := t.c.Get(u)
	if err != nil {
		t.t.Fatal(err)
	}
	defer res.Body.Close()
	slurp, err := io.ReadAll(res.Body)
	if err != nil {
		t.t.Fatal(err)
	}
	return string(slurp)
}

func (t *clientServerTest) scheme() string {
	if t.h2 {
		return "https"
	}
	return "http"
}

// newClientServerTest creates and starts an httptest.Server.
//
// The mode parameter selects the implementation to test:
// HTTP/1, HTTP/2, etc. Tests using newClientServerTest should use
// the 'run' function, which will start a subtests for each tested mode.
//
// The vararg opts parameter can include functions to configure the
// test server or transport.
//
// func(*httptest.Server) // run before starting the server
// func(*http.Transport)
func newClientServerTest(t testing.TB, mode testMode, h http.Handler, opts ...any) *clientServerTest {
	if mode == http2Mode {
		// CondSkipHTTP2(t)
	}
	cst := &clientServerTest{
		t:  t,
		h2: mode == http2Mode,
		h:  h,
	}
	cst.ts = httptest.NewUnstartedServer(h)

	var transportFuncs []func(*http.Transport)
	for _, opt := range opts {
		switch opt := opt.(type) {
		case func(*http.Transport):
			transportFuncs = append(transportFuncs, opt)
		case func(*httptest.Server):
			opt(cst.ts)
		default:
			t.Fatalf("unhandled option type %T", opt)
		}
	}

	if cst.ts.Config.ErrorLog == nil {
		cst.ts.Config.ErrorLog = log.New(testLogWriter{t}, "", 0)
	}

	switch mode {
	case http1Mode:
		cst.ts.Start()
	case https1Mode:
		cst.ts.StartTLS()
	case http2Mode:
		// ExportHttp2ConfigureServer(cst.ts.Config, nil)
		cst.ts.TLS = cst.ts.Config.TLSConfig
		cst.ts.StartTLS()
	default:
		t.Fatalf("unknown test mode %v", mode)
	}
	cst.c = cst.ts.Client()
	cst.tr = cst.c.Transport.(*http.Transport)
	if mode == http2Mode {
		// if err := ExportHttp2ConfigureTransport(cst.tr); err != nil {
		// 	t.Fatal(err)
		// }
	}
	for _, f := range transportFuncs {
		f(cst.tr)
	}
	t.Cleanup(func() {
		cst.close()
	})
	return cst
}

type testLogWriter struct {
	t testing.TB
}

func (w testLogWriter) Write(b []byte) (int, error) {
	w.t.Logf("server log: %v", strings.TrimSpace(string(b)))
	return len(b), nil
}

type stringHandler string

func (s stringHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Result", string(s))
}

var handlers = []struct {
	pattern string
	msg     string
}{
	{"/", "Default"},
	{"/someDir/", "someDir"},
	{"/#/", "hash"},
	{"someHost.com/someDir/", "someHost.com/someDir"},
}

var vtests = []struct {
	url      string
	expected string
}{
	{"http://localhost/someDir/apage", "someDir"},
	{"http://localhost/%23/apage", "hash"},
	{"http://localhost/otherDir/apage", "Default"},
	{"http://someHost.com/someDir/apage", "someHost.com/someDir"},
	{"http://otherHost.com/someDir/apage", "someDir"},
	{"http://otherHost.com/aDir/apage", "Default"},
	// redirections for trees
	{"http://localhost/someDir", "/someDir/"},
	{"http://localhost/%23", "/%23/"},
	{"http://someHost.com/someDir", "/someDir/"},
}

func TestHostHandlers(t *testing.T) { run(t, testHostHandlers, []testMode{http1Mode}) }
func testHostHandlers(t *testing.T, mode testMode) {
	mux := NewServeMux()
	for _, h := range handlers {
		mux.Handle(h.pattern, stringHandler(h.msg))
	}
	ts := newClientServerTest(t, mode, mux).ts

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cc := httputil.NewClientConn(conn, nil)
	for _, vt := range vtests {
		var r *http.Response
		var req http.Request
		if req.URL, err = url.Parse(vt.url); err != nil {
			t.Errorf("cannot parse url: %v", err)
			continue
		}
		if err := cc.Write(&req); err != nil {
			t.Errorf("writing request: %v", err)
			continue
		}
		r, err := cc.Read(&req)
		if err != nil {
			t.Errorf("reading response: %v", err)
			continue
		}
		switch r.StatusCode {
		case http.StatusOK:
			s := r.Header.Get("Result")
			if s != vt.expected {
				t.Errorf("Get(%q) = %q, want %q", vt.url, s, vt.expected)
			}
		case http.StatusMovedPermanently:
			s := r.Header.Get("Location")
			if s != vt.expected {
				t.Errorf("Get(%q) = %q, want %q", vt.url, s, vt.expected)
			}
		default:
			t.Errorf("Get(%q) unhandled status code %d", vt.url, r.StatusCode)
		}
	}
}

var serveMuxRegister = []struct {
	pattern string
	h       http.Handler
}{
	{"/dir/", serve(200)},
	{"/search", serve(201)},
	{"codesearch.google.com/search", serve(202)},
	{"codesearch.google.com/", serve(203)},
	{"example.com/", http.HandlerFunc(checkQueryStringHandler)},
}

// serve returns a handler that sends a response with the given code.
func serve(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}
}

// checkQueryStringHandler checks if r.URL.RawQuery has the same value
// as the URL excluding the scheme and the query string and sends 200
// response code if it is, 500 otherwise.
func checkQueryStringHandler(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Scheme = "http"
	u.Host = r.Host
	u.RawQuery = ""
	if "http://"+r.URL.RawQuery == u.String() {
		w.WriteHeader(200)
	} else {
		w.WriteHeader(500)
	}
}

var serveMuxTests = []struct {
	method  string
	host    string
	path    string
	code    int
	pattern string
}{
	{"GET", "google.com", "/", 404, ""},
	{"GET", "google.com", "/dir", 301, "/dir/"},
	{"GET", "google.com", "/dir/", 200, "/dir/"},
	{"GET", "google.com", "/dir/file", 200, "/dir/"},
	{"GET", "google.com", "/search", 201, "/search"},
	{"GET", "google.com", "/search/", 404, ""},
	{"GET", "google.com", "/search/foo", 404, ""},
	{"GET", "codesearch.google.com", "/search", 202, "codesearch.google.com/search"},
	{"GET", "codesearch.google.com", "/search/", 203, "codesearch.google.com/"},
	{"GET", "codesearch.google.com", "/search/foo", 203, "codesearch.google.com/"},
	{"GET", "codesearch.google.com", "/", 203, "codesearch.google.com/"},
	{"GET", "codesearch.google.com:443", "/", 203, "codesearch.google.com/"},
	{"GET", "images.google.com", "/search", 201, "/search"},
	{"GET", "images.google.com", "/search/", 404, ""},
	{"GET", "images.google.com", "/search/foo", 404, ""},
	{"GET", "google.com", "/../search", 301, "/search"},
	{"GET", "google.com", "/dir/..", 301, ""},
	{"GET", "google.com", "/dir/..", 301, ""},
	{"GET", "google.com", "/dir/./file", 301, "/dir/"},

	// The /foo -> /foo/ redirect applies to CONNECT requests
	// but the path canonicalization does not.
	{"CONNECT", "google.com", "/dir", 301, "/dir/"},
	{"CONNECT", "google.com", "/../search", 404, ""},
	{"CONNECT", "google.com", "/dir/..", 200, "/dir/"},
	{"CONNECT", "google.com", "/dir/..", 200, "/dir/"},
	{"CONNECT", "google.com", "/dir/./file", 200, "/dir/"},
}

func TestServeMuxHandler(t *testing.T) {
	setParallel(t)
	mux := NewServeMux()
	for _, e := range serveMuxRegister {
		mux.Handle(e.pattern, e.h)
	}

	for _, tt := range serveMuxTests {
		r := &http.Request{
			Method: tt.method,
			Host:   tt.host,
			URL: &url.URL{
				Path: tt.path,
			},
		}
		h, pattern := mux.Handler(r)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if pattern != tt.pattern || rr.Code != tt.code {
			t.Errorf("%s %s %s = %d, %q, want %d, %q", tt.method, tt.host, tt.path, rr.Code, pattern, tt.code, tt.pattern)
		}
	}
}

// Issue 24297
func TestServeMuxHandleFuncWithNilHandler(t *testing.T) {
	setParallel(t)
	defer func() {
		if err := recover(); err == nil {
			t.Error("expected call to mux.HandleFunc to panic")
		}
	}()
	mux := NewServeMux()
	mux.HandleFunc("/", nil)
}

var serveMuxTests2 = []struct {
	method  string
	host    string
	url     string
	code    int
	redirOk bool
}{
	{"GET", "google.com", "/", 404, false},
	{"GET", "example.com", "/test/?example.com/test/", 200, false},
	{"GET", "example.com", "test/?example.com/test/", 200, true},
}

// TestServeMuxHandlerRedirects tests that automatic redirects generated by
// mux.Handler() shouldn't clear the request's query string.
func TestServeMuxHandlerRedirects(t *testing.T) {
	setParallel(t)
	mux := NewServeMux()
	for _, e := range serveMuxRegister {
		mux.Handle(e.pattern, e.h)
	}

	for _, tt := range serveMuxTests2 {
		tries := 1 // expect at most 1 redirection if redirOk is true.
		turl := tt.url
		for {
			u, e := url.Parse(turl)
			if e != nil {
				t.Fatal(e)
			}
			r := &http.Request{
				Method: tt.method,
				Host:   tt.host,
				URL:    u,
			}
			h, _ := mux.Handler(r)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, r)
			if rr.Code != 301 {
				if rr.Code != tt.code {
					t.Errorf("%s %s %s = %d, want %d", tt.method, tt.host, tt.url, rr.Code, tt.code)
				}
				break
			}
			if !tt.redirOk {
				t.Errorf("%s %s %s, unexpected redirect", tt.method, tt.host, tt.url)
				break
			}
			turl = rr.HeaderMap.Get("Location")
			tries--
		}
		if tries < 0 {
			t.Errorf("%s %s %s, too many redirects", tt.method, tt.host, tt.url)
		}
	}
}

// Tests for https://golang.org/issue/900
func TestMuxRedirectLeadingSlashes(t *testing.T) {
	setParallel(t)
	paths := []string{"//foo.txt", "///foo.txt", "/../../foo.txt"}
	for _, path := range paths {
		req, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + path + " HTTP/1.1\r\nHost: test\r\n\r\n")))
		if err != nil {
			t.Errorf("%s", err)
		}
		mux := NewServeMux()
		resp := httptest.NewRecorder()

		mux.ServeHTTP(resp, req)

		if loc, expected := resp.Header().Get("Location"), "/foo.txt"; loc != expected {
			t.Errorf("Expected Location header set to %q; got %q", expected, loc)
			return
		}

		if code, expected := resp.Code, http.StatusMovedPermanently; code != expected {
			t.Errorf("Expected response code of StatusMovedPermanently; got %d", code)
			return
		}
	}
}

// Test that the special cased "/route" redirect
// implicitly created by a registered "/route/"
// properly sets the query string in the redirect URL.
// See Issue 17841.
func TestServeWithSlashRedirectKeepsQueryString(t *testing.T) {
	run(t, testServeWithSlashRedirectKeepsQueryString, []testMode{http1Mode})
}
func testServeWithSlashRedirectKeepsQueryString(t *testing.T, mode testMode) {
	writeBackQuery := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s", r.URL.RawQuery)
	}

	mux := NewServeMux()
	mux.HandleFunc("/testOne", writeBackQuery)
	mux.HandleFunc("/testTwo/", writeBackQuery)
	mux.HandleFunc("/testThree", writeBackQuery)
	mux.HandleFunc("/testThree/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s:bar", r.URL.RawQuery)
	})

	ts := newClientServerTest(t, mode, mux).ts

	tests := [...]struct {
		path     string
		method   string
		want     string
		statusOk bool
	}{
		0: {"/testOne?this=that", "GET", "this=that", true},
		1: {"/testTwo?foo=bar", "GET", "foo=bar", true},
		2: {"/testTwo?a=1&b=2&a=3", "GET", "a=1&b=2&a=3", true},
		3: {"/testTwo?", "GET", "", true},
		4: {"/testThree?foo", "GET", "foo", true},
		5: {"/testThree/?foo", "GET", "foo:bar", true},
		6: {"/testThree?foo", "CONNECT", "foo", true},
		7: {"/testThree/?foo", "CONNECT", "foo:bar", true},

		// canonicalization or not
		8: {"/testOne/foo/..?foo", "GET", "foo", true},
		9: {"/testOne/foo/..?foo", "CONNECT", "404 page not found\n", false},
	}

	for i, tt := range tests {
		req, _ := http.NewRequest(tt.method, ts.URL+tt.path, nil)
		res, err := ts.Client().Do(req)
		if err != nil {
			continue
		}
		slurp, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !tt.statusOk {
			if got, want := res.StatusCode, 404; got != want {
				t.Errorf("#%d: Status = %d; want = %d", i, got, want)
			}
		}
		if got, want := string(slurp), tt.want; got != want {
			t.Errorf("#%d: Body = %q; want = %q", i, got, want)
		}
	}
}

func TestServeWithSlashRedirectForHostPatterns(t *testing.T) {
	setParallel(t)

	mux := NewServeMux()
	mux.Handle("example.com/pkg/foo/", stringHandler("example.com/pkg/foo/"))
	mux.Handle("example.com/pkg/bar", stringHandler("example.com/pkg/bar"))
	mux.Handle("example.com/pkg/bar/", stringHandler("example.com/pkg/bar/"))
	mux.Handle("example.com:3000/pkg/connect/", stringHandler("example.com:3000/pkg/connect/"))
	mux.Handle("example.com:9000/", stringHandler("example.com:9000/"))
	mux.Handle("/pkg/baz/", stringHandler("/pkg/baz/"))

	tests := []struct {
		method string
		url    string
		code   int
		loc    string
		want   string
	}{
		{"GET", "http://example.com/", 404, "", ""},
		{"GET", "http://example.com/pkg/foo", 301, "/pkg/foo/", ""},
		{"GET", "http://example.com/pkg/bar", 200, "", "example.com/pkg/bar"},
		{"GET", "http://example.com/pkg/bar/", 200, "", "example.com/pkg/bar/"},
		{"GET", "http://example.com/pkg/baz", 301, "/pkg/baz/", ""},
		{"GET", "http://example.com:3000/pkg/foo", 301, "/pkg/foo/", ""},
		{"CONNECT", "http://example.com/", 404, "", ""},
		{"CONNECT", "http://example.com:3000/", 404, "", ""},
		{"CONNECT", "http://example.com:9000/", 200, "", "example.com:9000/"},
		{"CONNECT", "http://example.com/pkg/foo", 301, "/pkg/foo/", ""},
		{"CONNECT", "http://example.com:3000/pkg/foo", 404, "", ""},
		{"CONNECT", "http://example.com:3000/pkg/baz", 301, "/pkg/baz/", ""},
		{"CONNECT", "http://example.com:3000/pkg/connect", 301, "/pkg/connect/", ""},
	}

	for i, tt := range tests {
		req, _ := http.NewRequest(tt.method, tt.url, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if got, want := w.Code, tt.code; got != want {
			t.Errorf("#%d: Status = %d; want = %d", i, got, want)
		}

		if tt.code == 301 {
			if got, want := w.HeaderMap.Get("Location"), tt.loc; got != want {
				t.Errorf("#%d: Location = %q; want = %q", i, got, want)
			}
		} else {
			if got, want := w.HeaderMap.Get("Result"), tt.want; got != want {
				t.Errorf("#%d: Result = %q; want = %q", i, got, want)
			}
		}
	}
}

func TestShouldRedirectConcurrency(t *testing.T) { run(t, testShouldRedirectConcurrency) }
func testShouldRedirectConcurrency(t *testing.T, mode testMode) {
	mux := NewServeMux()
	newClientServerTest(t, mode, mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
}

func TestMuxRedirectRelative(t *testing.T) {
	setParallel(t)
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET http://example.com HTTP/1.1\r\nHost: test\r\n\r\n")))
	if err != nil {
		t.Errorf("%s", err)
	}
	mux := NewServeMux()
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if got, want := resp.Header().Get("Location"), "/"; got != want {
		t.Errorf("Location header expected %q; got %q", want, got)
	}
	if got, want := resp.Code, http.StatusMovedPermanently; got != want {
		t.Errorf("Expected response code %d; got %d", want, got)
	}
}
