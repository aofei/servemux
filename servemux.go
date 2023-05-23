package servemux

import (
	"context"
	"fmt"
	"net/http"
	stdpath "path"
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
	registeredPatterns map[string]string
	maxPathVars        int
	pathVarValuesPool  sync.Pool
}

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux() *ServeMux { return new(ServeMux) }

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
		mux.registeredPatterns = map[string]string{}
	}

	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = method
		method = ""
	}

	for _, c := range method {
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			panic("http.ServeMux: pattern method must be alphanumeric")
		}
	}

	if path == "" {
		panic("http.ServeMux: pattern path cannot be empty")
	}
	if path[0] != '/' {
		panic("http.ServeMux: pattern path must start with '/'")
	}

	hasTrailingSlash := path[len(path)-1] == '/'
	path = stdpath.Clean(path)
	if hasTrailingSlash && path != "/" {
		path += "/{...}"
	}
	path = strings.TrimSuffix(path, "{$}")
	if strings.Contains(path, "{$}") {
		panic("http.ServeMux: \"{$}\" can only appear at the end of a pattern path")
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

	cleanPattern := method + path
	for i := len(method); i < len(cleanPattern); i++ {
		if cleanPattern[i] == '{' {
			j := i + 1
			for ; i < len(cleanPattern) && cleanPattern[i] != '}'; i++ {
			}
			if strings.HasSuffix(cleanPattern[j:i], "...") {
				cleanPattern = cleanPattern[:j] + cleanPattern[i-3:]
				i = j + 4
			} else {
				cleanPattern = cleanPattern[:j] + cleanPattern[i:]
				i = j + 1
			}
		}
	}

	if registeredPattern, ok := mux.registeredPatterns[cleanPattern]; ok {
		panic(fmt.Sprintf("http.ServeMux: pattern %q conflicts with %q", pattern, registeredPattern))
	} else {
		mux.registeredPatterns[cleanPattern] = pattern
	}

	var pathVarNames []string
	for i := 0; i < len(path); i++ {
		if path[i] != '{' {
			continue
		}

		mux.insert(pattern, method, path[:i], nil, staticServeMuxNode, nil)

		j := i + 1
		for ; i < len(path) && path[i] != '}'; i++ {
		}

		pathVarName := path[j:i]

		nodeType := varServeMuxNode
		if strings.HasSuffix(pathVarName, "...") {
			nodeType = wildcardVarServeMuxNode
			pathVarName = strings.TrimSuffix(pathVarName, "...")
		}

		if pathVarName != "" {
			for _, pvn := range pathVarNames {
				if pvn == pathVarName {
					panic("http.ServeMux: pattern path cannot have duplicate variable names")
				}
			}
		}

		pathVarNames = append(pathVarNames, pathVarName)

		if nodeType == wildcardVarServeMuxNode {
			path = path[:j] + path[i-3:]
			i = j + 4
		} else {
			path = path[:j] + path[i:]
			i = j + 1
		}
		if i < len(path) {
			mux.insert(pattern, method, path[:i], nil, nodeType, pathVarNames)
		} else {
			mux.insert(pattern, method, path[:i], handler, nodeType, pathVarNames)
			if nodeType == wildcardVarServeMuxNode {
				i = j - 1
				if i > 1 && len(pathVarNames) == 1 {
					method, path := "_tsr", path[:i-1]
					cleanPattern := method + path
					if _, ok := mux.registeredPatterns[cleanPattern]; !ok {
						mux.registeredPatterns[cleanPattern] = pattern
						mux.insert(pattern, method, path, mux.tsrHandler(), staticServeMuxNode, nil)
					}
				}
			}
			break
		}
	}

	mux.insert(pattern, method, path, handler, staticServeMuxNode, pathVarNames)
}

// insert inserts nodes into the mux.tree.
func (mux *ServeMux) insert(pattern, method, path string, h http.Handler, nt serveMuxNodeType, pathVarNames []string) {
	if l := len(pathVarNames); mux.maxPathVars < l {
		mux.maxPathVars = l
		mux.pathVarValuesPool = sync.Pool{New: func() any { return make([]string, l) }}
	}

	var (
		s  = path        // Search
		sl int           // Search length
		pl int           // Prefix length
		ll int           // LCP length
		ml int           // Minimum length of the sl and pl
		cn = mux.tree    // Current node
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

		if ll == 0 { // At root node
			cn.prefix = s
			cn.label = s[0]
			if h != nil {
				cn.typ = nt
				cn.pathVarNames = pathVarNames
				cn.setHandler(method, pattern, h)
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
				pathVarNames:         cn.pathVarNames,
				handlers:             cn.handlers,
				catchAllHandler:      cn.catchAllHandler,
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
			cn.pathVarNames = nil
			cn.handlers = nil
			cn.catchAllHandler = nil
			cn.hasAtLeastOneHandler = false
			cn.addChild(nn)

			if ll == sl { // At current node
				cn.typ = nt
				cn.pathVarNames = pathVarNames
				cn.setHandler(method, pattern, h)
			} else { // Create child node
				nn = &serveMuxNode{
					prefix:         s[ll:],
					label:          s[ll],
					typ:            nt,
					parent:         cn,
					staticChildren: make([]*serveMuxNode, 255),
					pathVarNames:   pathVarNames,
				}
				nn.setHandler(method, pattern, h)
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
				pathVarNames:   pathVarNames,
			}
			nn.setHandler(method, pattern, h)
			cn.addChild(nn)
		} else if h != nil { // Node already exists
			if len(cn.pathVarNames) == 0 {
				cn.pathVarNames = pathVarNames
			}
			cn.setHandler(method, pattern, h)
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
	mux.mu.RLock()
	defer mux.mu.RUnlock()

	if mux.tree == nil {
		return mux.notFoundHandler(), ""
	}

	var (
		s    = r.URL.Path     // Search
		si   int              // Search index
		sl   int              // Search length
		pl   int              // Prefix length
		ll   int              // LCP length
		ml   int              // Minimum length of the sl and pl
		cn   = mux.tree       // Current node
		sn   *serveMuxNode    // Saved node
		fnt  serveMuxNodeType // From node type
		nnt  serveMuxNodeType // Next node type
		pvi  int              // Path variable index
		pvvs []string         // Path variable values
		i    int              // Index
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

			if pattern, h = cn.handler(r.Method); h != nil {
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

			if pattern, h = cn.handler(r.Method); h != nil {
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

			s = r.URL.Path[si:]
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

	if cn == nil || h == nil {
		if pvvs != nil {
			//lint:ignore SA6002 this is harmless
			mux.pathVarValuesPool.Put(pvvs)
		}

		if sn != nil && sn.hasAtLeastOneHandler {
			return mux.methodNotAllowedHandler(), ""
		}

		return mux.notFoundHandler(), ""
	}

	if len(cn.pathVarNames) > 0 {
		if pathVars, ok := r.Context().Value(pathVarsContextKey).(map[string]string); ok {
			for pvi, pvn := range cn.pathVarNames {
				if pvn != "" {
					pathVars[pvn] = pvvs[pvi]
				}
			}
		}
		//lint:ignore SA6002 this is harmless
		mux.pathVarValuesPool.Put(pvvs)
	}

	return
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
		requestURI := r.RequestURI
		if requestURI == "" {
			requestURI = "/"
		} else {
			path, query := requestURI, ""
			for i := 0; i < len(path); i++ {
				if path[i] == '?' {
					query = path[i:]
					path = path[:i]
					break
				}
			}
			if path == "" || path[len(path)-1] != '/' {
				path += "/"
			}
			requestURI = path + query
		}
		http.Redirect(w, r, requestURI, http.StatusMovedPermanently)
	})
}

// serveMuxNode is a node of the radix tree of a [ServeMux].
type serveMuxNode struct {
	prefix               string
	label                byte
	typ                  serveMuxNodeType
	parent               *serveMuxNode
	staticChildren       []*serveMuxNode
	varChild             *serveMuxNode
	wildcardVarChild     *serveMuxNode
	hasAtLeastOneChild   bool
	pathVarNames         []string
	handlers             map[string]*methodHandlerPatternTuple
	catchAllHandler      *methodHandlerPatternTuple
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

// handler returns a pattern and [http.Handler] in the mn for the method. It
// returns "", nil if not found.
func (mn *serveMuxNode) handler(method string) (pattern string, h http.Handler) {
	if h := mn.handlers[method]; h != nil {
		return h.pattern, h.handler
	}
	if mn.catchAllHandler != nil {
		return mn.catchAllHandler.pattern, mn.catchAllHandler.handler
	}
	return "", nil
}

// setHandler sets the h with pattern to the mn based on the method.
func (mn *serveMuxNode) setHandler(method, pattern string, h http.Handler) {
	if mn.handlers == nil {
		mn.handlers = map[string]*methodHandlerPatternTuple{}
	}
	mhpt := &methodHandlerPatternTuple{method, h, pattern}
	switch method {
	case "", "_tsr":
		if method == "_tsr" && mn.hasAtLeastOneHandler {
			return
		}
		mn.catchAllHandler = mhpt
	default:
		mn.handlers[method] = mhpt
	}
	if method != "_tsr" &&
		len(mn.handlers) > 0 &&
		mn.catchAllHandler != nil &&
		mn.catchAllHandler.method == "_tsr" {
		mn.catchAllHandler = nil
	}
	mn.hasAtLeastOneHandler = len(mn.handlers) > 0 || mn.catchAllHandler != nil
}

// serveMuxNodeType is a type of a [serveMuxNode].
type serveMuxNodeType uint8

// The types of [serveMuxNode].
const (
	staticServeMuxNode serveMuxNodeType = iota
	varServeMuxNode
	wildcardVarServeMuxNode
)

// methodHandlerPatternTuple is a {method,handler,pattern} tuple.
type methodHandlerPatternTuple struct {
	method  string
	handler http.Handler
	pattern string
}
