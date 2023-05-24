package servemux

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
)

// contextKey is a key for a context value.
type contextKey struct{ name string }

// The context keys.
var pathVarsContextKey = &contextKey{"path-vars"}

// PathVars returns path variables of the r for the name. It returns nil if not
// found.
func PathVars(r *http.Request) map[string]string {
	pathVars, ok := r.Context().Value(pathVarsContextKey).(map[string]string)
	if !ok {
		return nil
	}
	return pathVars
}

// ConfigureRequestToStorePathVars configures the r so that it can be used to
// store path variables.
func ConfigureRequestToStorePathVars(r *http.Request) *http.Request {
	if _, ok := r.Context().Value(pathVarsContextKey).(map[string]string); ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), pathVarsContextKey, map[string]string{}))
}

// ServeMux is an HTTP request multiplexer. It matches the URL of each incoming
// request against a list of registered patterns and calls the handler for the
// pattern that most closely matches the URL.
//
// ...
type ServeMux struct {
	mu                 sync.RWMutex
	tree               *serveMuxNode
	hostTrees          map[string]*serveMuxNode
	registeredPatterns map[string]string
	maxPathVars        int
	pathVarValuesPool  sync.Pool
}

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux() *ServeMux { return new(ServeMux) }

var (
	// serveMuxMethodRE is used to match valid method for the
	// [ServeMux.parsePattern].
	serveMuxMethodRE = regexp.MustCompile(`^[0-9A-Za-z]+$`)

	// serveMuxPathVarNameRE is used to match valid path variable name for
	// the [ServeMux.parsePattern].
	serveMuxPathVarNameRE = regexp.MustCompile(`^[_\pL][_\pL\p{Nd}]*$`)
)

// parsePattern parses the pattern. It panics when something goes wrong.
func (mux *ServeMux) parsePattern(pattern string) (method, host, path string, pathVarNames []string) {
	method, hostpath, ok := strings.Cut(pattern, " ")
	if !ok {
		hostpath = method
		method = ""
	}

	if method != "" && !serveMuxMethodRE.MatchString(method) {
		panic("http.ServeMux: pattern method must be alphanumeric")
	}

	if hostpath == "" {
		panic("http.ServeMux: pattern hostpath cannot be empty")
	}
	if hostpath[0] == '/' {
		path = hostpath
	} else if i := strings.Index(hostpath, "/"); i > 0 {
		host = hostpath[:i]
		path = hostpath[i:]
	} else {
		host = hostpath
		path = ""
	}

	if host != "" {
		u, _ := url.Parse("http://" + host + "/")
		if u == nil || u.Host != host {
			panic("http.ServeMux: invalid pattern host")
		}
	}

	if path != "" {
		path = cleanPath(path)
		if path[len(path)-1] == '/' {
			path += "{...}"
		}
		path = strings.TrimSuffix(path, "{$}")
		if strings.Contains(path, "{$}") {
			panic(`http.ServeMux: "{$}" can only appear at the end of a pattern path`)
		}

		if strings.Contains(path, "{") {
			for _, p := range strings.Split(path, "/") {
				if strings.Count(p, "{") > 1 {
					panic("http.ServeMux: only one variable is allowed in a single path element of pattern")
				}
				if len(p) > 0 && p[0] == '{' && p[len(p)-1] != '}' {
					panic("http.ServeMux: a path element of pattern must either be a variable or not")
				}
			}
			if c := strings.Count(path, "...}"); c > 1 {
				panic("http.ServeMux: only one wildcard variable is allowed in a pattern path")
			} else if c == 1 && !strings.HasSuffix(path, "...}") {
				panic("http.ServeMux: wildcard variable can only appear at the end of a pattern path")
			}
		}

		for i := 0; i < len(path); i++ {
			if path[i] != '{' {
				continue
			}

			j := i + 1
			for ; i < len(path) && path[i] != '}'; i++ {
			}

			pathVarName := path[j:i]
			isWildcard := false
			if strings.HasSuffix(pathVarName, "...") {
				isWildcard = true
				pathVarName = strings.TrimSuffix(pathVarName, "...")
			}
			if pathVarName != "" {
				if !serveMuxPathVarNameRE.MatchString(pathVarName) {
					panic("http.ServeMux: a path variable name in pattern must be either empty or a valid Go identifier")
				}
				for _, pvn := range pathVarNames {
					if pvn == pathVarName {
						panic("http.ServeMux: pattern path cannot have duplicate variable names")
					}
				}
			}
			pathVarNames = append(pathVarNames, pathVarName)

			if isWildcard {
				path = path[:j] + path[i-3:]
				i = j + 4
			} else {
				path = path[:j] + path[i:]
				i = j + 1
			}
		}
	}

	denamedPattern := method + " " + host + path
	if registeredPattern, ok := mux.registeredPatterns[denamedPattern]; ok {
		panic(fmt.Sprintf("http.ServeMux: pattern %q conflicts with %q", pattern, registeredPattern))
	} else {
		mux.registeredPatterns[denamedPattern] = pattern
	}

	return
}

// Handle registers the handler for the given pattern. If a handler already
// exists for pattern, Handle panics.
//
// ...
func (mux *ServeMux) Handle(pattern string, handler http.Handler) {
	mux.mu.Lock()
	defer mux.mu.Unlock()

	if pattern == "" {
		panic("http.ServeMux: empty pattern")
	}
	if handler == nil {
		panic("http.ServeMux: nil handler")
	}

	if mux.tree == nil {
		mux.tree = &serveMuxNode{staticChildren: make([]*serveMuxNode, 255)}
		mux.hostTrees = map[string]*serveMuxNode{}
		mux.registeredPatterns = map[string]string{}
	}

	method, host, path, pathVarNames := mux.parsePattern(pattern)

	tree := mux.tree
	if host != "" {
		tree = mux.hostTrees[host]
		if tree == nil {
			tree = &serveMuxNode{staticChildren: make([]*serveMuxNode, 255)}
			mux.hostTrees[host] = tree
		}
	}

	if l := len(pathVarNames); mux.maxPathVars < l {
		mux.maxPathVars = l
		mux.pathVarValuesPool = sync.Pool{New: func() any { return make([]string, l) }}
	}

	ht := &handlerTuple{method, pathVarNames, pattern, handler}
	for i := 0; i < len(path); i++ {
		if path[i] != '{' {
			continue
		}

		mux.insert(tree, staticServeMuxNode, path[:i], nil)

		j := i + 1
		for ; i < len(path) && path[i] != '}'; i++ {
		}

		nodeType := varServeMuxNode
		if path[j:i] == "..." {
			nodeType = wildcardVarServeMuxNode
		}

		i++
		if i < len(path) {
			mux.insert(tree, nodeType, path[:i], nil)
		} else {
			mux.insert(tree, nodeType, path, ht)
			if nodeType == wildcardVarServeMuxNode {
				i = j - 1
				if i > 1 && len(pathVarNames) == 1 {
					method, path := "_tsr", path[:i-1]
					denamedPattern := method + " " + host + path
					if _, ok := mux.registeredPatterns[denamedPattern]; !ok {
						mux.registeredPatterns[denamedPattern] = pattern
						mux.insert(tree, staticServeMuxNode, path, &handlerTuple{
							method:  method,
							pattern: pattern,
							handler: mux.tsrHandler(),
						})
					}
				}
			}
			break
		}
	}
	mux.insert(tree, staticServeMuxNode, path, ht)
}

// insert inserts nodes into the tree.
func (mux *ServeMux) insert(tree *serveMuxNode, nt serveMuxNodeType, path string, ht *handlerTuple) {
	var (
		s  = path        // Search
		sl int           // Search length
		pl int           // Prefix length
		ll int           // LCP length
		ml int           // Minimum length of the sl and pl
		cn = tree        // Current node
		nn *serveMuxNode // Next node
	)

	for {
		sl, pl, ll = len(s), len(cn.prefix), 0
		if sl < pl {
			ml = sl
		} else {
			ml = pl
		}

		for ; ll < ml && s[ll] == cn.prefix[ll]; ll++ {
		}

		if s == "" { // At root node
			if ht != nil {
				cn.typ = nt
				cn.setHandlerTuple(ht)
			}
		} else if ll < pl { // Split node
			nn = &serveMuxNode{
				prefix:               cn.prefix[ll:],
				label:                cn.prefix[ll],
				typ:                  cn.typ,
				parent:               cn,
				staticChildren:       cn.staticChildren,
				varChild:             cn.varChild,
				wildcardVarChild:     cn.wildcardVarChild,
				hasAtLeastOneChild:   cn.hasAtLeastOneChild,
				handlerTuples:        cn.handlerTuples,
				catchAllHandlerTuple: cn.catchAllHandlerTuple,
				hasAtLeastOneHandler: cn.hasAtLeastOneHandler,
			}

			for _, n := range nn.staticChildren {
				if n != nil {
					n.parent = nn
				}
			}

			if nn.varChild != nil {
				nn.varChild.parent = nn
			}

			if nn.wildcardVarChild != nil {
				nn.wildcardVarChild.parent = nn
			}

			// Reset current node.
			cn.prefix = cn.prefix[:ll]
			cn.label = cn.prefix[0]
			cn.typ = staticServeMuxNode
			cn.staticChildren = make([]*serveMuxNode, 255)
			cn.varChild = nil
			cn.wildcardVarChild = nil
			cn.hasAtLeastOneChild = false
			cn.handlerTuples = nil
			cn.catchAllHandlerTuple = nil
			cn.hasAtLeastOneHandler = false
			cn.addChild(nn)

			if ll == sl { // At current node
				cn.typ = nt
				if ht != nil {
					cn.setHandlerTuple(ht)
				}
			} else { // Create child node
				nn = &serveMuxNode{
					prefix:         s[ll:],
					label:          s[ll],
					typ:            nt,
					parent:         cn,
					staticChildren: make([]*serveMuxNode, 255),
				}
				if ht != nil {
					nn.setHandlerTuple(ht)
				}
				cn.addChild(nn)
			}
		} else if ll < sl {
			s = s[ll:]

			nn = nil
			if s[0] != '{' {
				nn = cn.staticChildren[s[0]]
			} else if s[1] == '}' {
				nn = cn.varChild
			} else {
				nn = cn.wildcardVarChild
			}

			if nn != nil {
				// Go deeper.
				cn = nn
				continue
			}

			// Create child node.
			nn = &serveMuxNode{
				prefix:         s,
				label:          s[0],
				typ:            nt,
				parent:         cn,
				staticChildren: make([]*serveMuxNode, 255),
			}
			if ht != nil {
				nn.setHandlerTuple(ht)
			}
			cn.addChild(nn)
		} else if ht != nil { // Node already exists
			cn.setHandlerTuple(ht)
		}

		break
	}
}

// HandleFunc registers the handler function for the given pattern.
func (mux *ServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if handler == nil {
		panic("http.ServeMux: nil handler")
	}
	mux.Handle(pattern, http.HandlerFunc(handler))
}

// Handler returns the handler to use for the given request, consulting
// r.Method, r.Host, and r.URL.Path. It always returns a non-nil handler. If the
// path is not in its canonical form, the handler will be an
// internally-generated handler that redirects to the canonical path. If the
// host contains a port, it is ignored when matching handlers.
//
// ...
func (mux *ServeMux) Handler(r *http.Request) (h http.Handler, pattern string) {
	var path string
	if r.Method != http.MethodConnect {
		path = cleanPath(r.URL.Path)
	} else {
		path = r.URL.Path
	}
	h, pattern = mux.handler(path, r)
	if path != r.URL.Path {
		u := &url.URL{Path: path, RawQuery: r.URL.RawQuery}
		return http.RedirectHandler(u.String(), http.StatusMovedPermanently), pattern
	}
	return
}

// handler is the main implementation of the [mux.Handler].
func (mux *ServeMux) handler(path string, r *http.Request) (h http.Handler, pattern string) {
	mux.mu.RLock()
	defer mux.mu.RUnlock()
	if len(mux.hostTrees) > 0 {
		var tree *serveMuxNode
		if r.Method != http.MethodConnect {
			tree = mux.hostTrees[stripHostPort(r.Host)]
		} else {
			tree = mux.hostTrees[r.Host]
		}
		if tree != nil {
			if h, pattern = mux.search(tree, path, r); h != nil {
				return
			}
		}
	}
	if mux.tree != nil {
		if h, pattern = mux.search(mux.tree, path, r); h != nil {
			return
		}
	}
	return mux.notFoundHandler(), ""
}

// search searches the tree.
func (mux *ServeMux) search(tree *serveMuxNode, path string, r *http.Request) (h http.Handler, pattern string) {
	var (
		s    = path           // Search
		si   int              // Search index
		sl   int              // Search length
		pl   int              // Prefix length
		ll   int              // LCP length
		ml   int              // Minimum length of the sl and pl
		cn   = tree           // Current node
		sn   *serveMuxNode    // Saved node
		fnt  serveMuxNodeType // From node type
		nnt  serveMuxNodeType // Next node type
		pvi  int              // Path variable index
		pvvs []string         // Path variable values
		i    int              // Index
		ht   *handlerTuple    // Handler tuple
	)

	// Node search order: static > variable > wildcard variable.
OuterLoop:
	for {
		if cn.typ == staticServeMuxNode {
			sl, pl = len(s), len(cn.prefix)
			if sl < pl {
				ml = sl
			} else {
				ml = pl
			}

			ll = 0
			for ; ll < ml && s[ll] == cn.prefix[ll]; ll++ {
			}

			if ll != pl {
				fnt = staticServeMuxNode
				goto BacktrackToPreviousNode
			}

			s = s[ll:]
			si += ll
		}

		if s == "" && cn.hasAtLeastOneHandler {
			if sn == nil {
				sn = cn
			}
			if ht = cn.handlerTupleByMethod(r.Method); ht != nil {
				break
			}
		}

		// Try static node.
		if s != "" && cn.staticChildren[s[0]] != nil {
			cn = cn.staticChildren[s[0]]
			continue OuterLoop
		}

		// Try variable node.
	TryVarNode:
		if cn.varChild != nil {
			cn = cn.varChild

			i, sl = 0, len(s)
			for ; i < sl && s[i] != '/'; i++ {
			}

			if pvvs == nil {
				pvvs = mux.pathVarValuesPool.Get().([]string)
			}

			pvvs[pvi] = s[:i]
			pvi++

			s = s[i:]
			si += i

			continue
		}

		// Try wildcard variable node.
	TryWildcardVarNode:
		if cn.wildcardVarChild != nil {
			cn = cn.wildcardVarChild

			if pvvs == nil {
				pvvs = mux.pathVarValuesPool.Get().([]string)
			}

			pvvs[pvi] = s
			pvi++

			si += len(s)
			s = ""

			if sn == nil {
				sn = cn
			}

			if ht = cn.handlerTupleByMethod(r.Method); ht != nil {
				break
			}
		}

		fnt = wildcardVarServeMuxNode

		// Backtrack to previous node.
	BacktrackToPreviousNode:
		if fnt != staticServeMuxNode {
			if cn.typ == staticServeMuxNode {
				si -= len(cn.prefix)
			} else {
				pvi--
				si -= len(pvvs[pvi])
			}

			s = path[si:]
		}

		if cn.typ < wildcardVarServeMuxNode {
			nnt = cn.typ + 1
		} else {
			nnt = staticServeMuxNode
		}

		cn = cn.parent
		if cn != nil {
			switch nnt {
			case varServeMuxNode:
				goto TryVarNode
			case wildcardVarServeMuxNode:
				goto TryWildcardVarNode
			}
		} else if fnt == staticServeMuxNode {
			sn = nil
		}

		break
	}

	if cn == nil || ht == nil {
		if pvvs != nil {
			//lint:ignore SA6002 this is harmless
			mux.pathVarValuesPool.Put(pvvs)
		}
		if sn != nil && sn.hasAtLeastOneHandler {
			return mux.methodNotAllowedHandler(), ""
		}
		return nil, ""
	}

	if len(ht.pathVarNames) > 0 {
		if pathVars, ok := r.Context().Value(pathVarsContextKey).(map[string]string); ok {
			for pvi, pvn := range ht.pathVarNames {
				if pvn != "" {
					pathVars[pvn] = pvvs[pvi]
				}
			}
		}
		//lint:ignore SA6002 this is harmless
		mux.pathVarValuesPool.Put(pvvs)
	}

	return ht.handler, ht.pattern
}

// ServeHTTP dispatches the request to the handler whose pattern most closely
// matches the request URL.
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.RequestURI == "*" {
		if r.ProtoAtLeast(1, 1) {
			w.Header().Set("Connection", "close")
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r = ConfigureRequestToStorePathVars(r)
	h, _ := mux.Handler(r)
	h.ServeHTTP(w, r)
}

// notFoundHandler returns an [http.Handler] to write not found responses.
func (mux *ServeMux) notFoundHandler() http.Handler {
	return http.NotFoundHandler()
}

// methodNotAllowedHandler returns an [http.Handler] to write method not allowed
// responses.
func (mux *ServeMux) methodNotAllowedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
	})
}

// tsrHandler returns an [http.Handler] to write TSR (Trailing Slash Redirect)
// responses.
func (mux *ServeMux) tsrHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := &url.URL{Path: r.URL.Path + "/", RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
	})
}

// serveMuxNode is a node of the radix tree of a [ServeMux].
type serveMuxNode struct {
	prefix string
	label  byte
	typ    serveMuxNodeType
	parent *serveMuxNode

	staticChildren     []*serveMuxNode
	varChild           *serveMuxNode
	wildcardVarChild   *serveMuxNode
	hasAtLeastOneChild bool

	handlerTuples        map[string]*handlerTuple
	catchAllHandlerTuple *handlerTuple
	hasAtLeastOneHandler bool
}

// addChild adds the n as a child node to the mn.
func (mn *serveMuxNode) addChild(n *serveMuxNode) {
	switch n.typ {
	case staticServeMuxNode:
		mn.staticChildren[n.label] = n
	case varServeMuxNode:
		mn.varChild = n
	case wildcardVarServeMuxNode:
		mn.wildcardVarChild = n
	}
	mn.hasAtLeastOneChild = true
}

// handlerTupleByMethod returns a [handlerTuple] in the mn for the method. It
// returns nil if not found.
func (mn *serveMuxNode) handlerTupleByMethod(method string) *handlerTuple {
	if ht := mn.handlerTuples[method]; ht != nil {
		return ht
	}
	return mn.catchAllHandlerTuple
}

// setHandlerTuple sets the ht to the mn.
func (mn *serveMuxNode) setHandlerTuple(ht *handlerTuple) {
	if mn.handlerTuples == nil {
		mn.handlerTuples = map[string]*handlerTuple{}
	}
	switch ht.method {
	case "", "_tsr":
		if ht.method == "_tsr" && mn.hasAtLeastOneHandler {
			return
		}
		mn.catchAllHandlerTuple = ht
	default:
		mn.handlerTuples[ht.method] = ht
	}
	if ht.method != "_tsr" &&
		len(mn.handlerTuples) > 0 &&
		mn.catchAllHandlerTuple != nil &&
		mn.catchAllHandlerTuple.method == "_tsr" {
		mn.catchAllHandlerTuple = nil
	}
	mn.hasAtLeastOneHandler = len(mn.handlerTuples) > 0 || mn.catchAllHandlerTuple != nil
}

// serveMuxNodeType is a type of a [serveMuxNode].
type serveMuxNodeType uint8

// The types of [serveMuxNode].
const (
	staticServeMuxNode serveMuxNodeType = iota
	varServeMuxNode
	wildcardVarServeMuxNode
)

// handlerTuple is a handler tuple.
type handlerTuple struct {
	method       string
	pathVarNames []string
	pattern      string
	handler      http.Handler
}

// stripHostPort returns h without any trailing ":<port>".
func stripHostPort(h string) string {
	// If no port on host, return unchanged
	if !strings.Contains(h, ":") {
		return h
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		return h // on error, return unchanged
	}
	return host
}

// cleanPath returns the canonical path for p, eliminating . and .. elements.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	// path.Clean removes trailing slash except for root;
	// put the trailing slash back if necessary.
	if p[len(p)-1] == '/' && np != "/" {
		// Fast path for common case of p being the string we want:
		if len(p) == len(np)+1 && strings.HasPrefix(p, np) {
			np = p
		} else {
			np += "/"
		}
	}
	return np
}
