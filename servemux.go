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
		method, hostpath = "", method
	}

	if method != "" && !serveMuxMethodRE.MatchString(method) {
		panic("http.ServeMux: a pattern method must be either empty or alphanumeric")
	}

	if hostpath == "" {
		panic("http.ServeMux: a pattern must have at least one of the host or path")
	}
	if i := strings.Index(hostpath, "/"); i >= 0 {
		host, path = hostpath[:i], hostpath[i:]
	} else {
		host = hostpath
	}

	if host != "" {
		u, _ := url.Parse("http://" + host + "/")
		if u == nil || u.Host != host {
			panic(`http.ServeMux: a pattern host must be able to be parsed using net/url.Parse("http://" + host + "/")`)
		}
	}

	if path != "" {
		if path[len(path)-1] == '/' {
			path += "{...}"
		}
		var denamedPath string
		walkPath(path, func(recentlyPassedSlashes, elem string, elemIndex int) bool {
			denamedPath += recentlyPassedSlashes

			if fc, lc := elem[0], elem[len(elem)-1]; fc != '{' && lc != '}' {
				denamedPath += elem
				return true
			} else if (fc == '{') != (lc == '}') {
				panic("http.ServeMux: each path element in a pattern path must either be a variable or not")
			}

			varName, varModifier := elem[1:len(elem)-1], ""
			if i := strings.IndexAny(varName, ".$"); i >= 0 {
				varName, varModifier = varName[:i], varName[i:]
			}

			if varName != "" {
				if !serveMuxPathVarNameRE.MatchString(varName) {
					panic("http.ServeMux: the name of a variable path element in a pattern path must be either empty or a Go identifier")
				}
				for _, pvn := range pathVarNames {
					if pvn == varName {
						panic("http.ServeMux: all variable path elements within the same pattern path must have unique names")
					}
				}
			}
			pathVarNames = append(pathVarNames, varName)

			isNotLastElem := elemIndex+len(elem) < len(path)
			switch varModifier {
			case "":
			case "...":
				if isNotLastElem {
					panic("http.ServeMux: a ...-modified variable can only be the last path element in a pattern path")
				}
			case "$":
				if isNotLastElem {
					panic("http.ServeMux: a $-modified variable can only be the last path element in a pattern path")
				}
				if varName != "" {
					panic("http.ServeMux: a $-modified variable path element in a pattern path must have no name")
				}
				pathVarNames = pathVarNames[:len(pathVarNames)-1]
				return false
			default:
				panic("http.ServeMux: the modifier of a variable path element in a pattern path can only be ... or $")
			}
			denamedPath += "{" + varModifier + "}"

			return true
		})
		path = denamedPath
	}

	cleanedPattern := method + " " + host + path
	if registeredPattern, ok := mux.registeredPatterns[cleanedPattern]; ok {
		panic(fmt.Sprintf("http.ServeMux: pattern %q conflicts with %q", pattern, registeredPattern))
	} else {
		mux.registeredPatterns[cleanedPattern] = pattern
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
		mux.tree = &serveMuxNode{nonvarChildren: make([]*serveMuxNode, 255)}
		mux.hostTrees = map[string]*serveMuxNode{}
		mux.registeredPatterns = map[string]string{}
	}

	method, host, path, pathVarNames := mux.parsePattern(pattern)

	tree := mux.tree
	if host != "" {
		tree = mux.hostTrees[host]
		if tree == nil {
			tree = &serveMuxNode{nonvarChildren: make([]*serveMuxNode, 255)}
			mux.hostTrees[host] = tree
		}
	}

	if l := len(pathVarNames); mux.maxPathVars < l {
		mux.maxPathVars = l
		mux.pathVarValuesPool = sync.Pool{New: func() any { return make([]string, l) }}
	}

	ht := &handlerTuple{method, pathVarNames, pattern, handler}
	walkPath(path, func(_, elem string, elemIndex int) bool {
		if elem[0] != '{' {
			return true
		}

		mux.insert(tree, nonvarServeMuxNode, path[:elemIndex], nil)

		nodeType := unmodifiedVarServeMuxNode
		if elem == "{...}" {
			nodeType = ellipsisModifiedVarServeMuxNode
		}

		if nextSlashIndex := elemIndex + len(elem); nextSlashIndex < len(path) {
			mux.insert(tree, nodeType, path[:nextSlashIndex], nil)
			return true
		}

		mux.insert(tree, nodeType, path, ht)

		// For patterns like "/subtree/{...}", we may need to redirect
		// request paths like "/subtree" to "/subtree/".
		if path := strings.TrimRight(path[:elemIndex-1], "/"); path != "" &&
			nodeType == ellipsisModifiedVarServeMuxNode && len(pathVarNames) == 1 {
			method := "_tsr"
			cleanedPattern := method + " " + host + path
			if _, ok := mux.registeredPatterns[cleanedPattern]; !ok {
				mux.registeredPatterns[cleanedPattern] = pattern
				mux.insert(tree, nonvarServeMuxNode, path, &handlerTuple{
					method:  method,
					pattern: pattern,
					handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						u := &url.URL{Path: r.URL.Path + "/", RawQuery: r.URL.RawQuery}
						http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
					}),
				})
			}
		}

		return false
	})
	mux.insert(tree, nonvarServeMuxNode, path, ht)
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
				prefix:                   cn.prefix[ll:],
				label:                    cn.prefix[ll],
				typ:                      cn.typ,
				parent:                   cn,
				nonvarChildren:           cn.nonvarChildren,
				unmodifiedVarChild:       cn.unmodifiedVarChild,
				ellipsisModifiedVarChild: cn.ellipsisModifiedVarChild,
				hasAtLeastOneChild:       cn.hasAtLeastOneChild,
				handlerTuples:            cn.handlerTuples,
				catchAllHandlerTuple:     cn.catchAllHandlerTuple,
				hasAtLeastOneHandler:     cn.hasAtLeastOneHandler,
			}

			for _, n := range nn.nonvarChildren {
				if n != nil {
					n.parent = nn
				}
			}

			if nn.unmodifiedVarChild != nil {
				nn.unmodifiedVarChild.parent = nn
			}

			if nn.ellipsisModifiedVarChild != nil {
				nn.ellipsisModifiedVarChild.parent = nn
			}

			// Reset current node.
			cn.prefix = cn.prefix[:ll]
			cn.label = cn.prefix[0]
			cn.typ = nonvarServeMuxNode
			cn.nonvarChildren = make([]*serveMuxNode, 255)
			cn.unmodifiedVarChild = nil
			cn.ellipsisModifiedVarChild = nil
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
					nonvarChildren: make([]*serveMuxNode, 255),
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
				nn = cn.nonvarChildren[s[0]]
			} else if s[1] == '}' {
				nn = cn.unmodifiedVarChild
			} else {
				nn = cn.ellipsisModifiedVarChild
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
				nonvarChildren: make([]*serveMuxNode, 255),
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
			if h, pattern = mux.match(tree, path, r); h != nil {
				return
			}
		}
	}
	if mux.tree != nil {
		if h, pattern = mux.match(mux.tree, path, r); h != nil {
			return
		}
	}
	return mux.notFoundHandler(), ""
}

// match finds the best match for the r from the tree.
func (mux *ServeMux) match(tree *serveMuxNode, path string, r *http.Request) (h http.Handler, pattern string) {
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

	// Node precedence: non-variable > unmodified variable > ...-modified variable.
OuterLoop:
	for {
		if cn.typ == nonvarServeMuxNode {
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
				fnt = nonvarServeMuxNode
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

		// Try non-variable node.
		if s != "" && cn.nonvarChildren[s[0]] != nil {
			cn = cn.nonvarChildren[s[0]]
			continue OuterLoop
		}

		// Try unmodified variable node.
	TryUnmodifiedVarNode:
		if cn.unmodifiedVarChild != nil {
			cn = cn.unmodifiedVarChild

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

		// Try ...-modified variable node.
	TryEllipsisModifiedVarNode:
		if cn.ellipsisModifiedVarChild != nil {
			cn = cn.ellipsisModifiedVarChild

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

		fnt = ellipsisModifiedVarServeMuxNode

		// Backtrack to the previous node.
	BacktrackToPreviousNode:
		if fnt != nonvarServeMuxNode {
			if cn.typ == nonvarServeMuxNode {
				si -= len(cn.prefix)
			} else {
				pvi--
				si -= len(pvvs[pvi])
			}

			s = path[si:]
		}

		if cn.typ < ellipsisModifiedVarServeMuxNode {
			nnt = cn.typ + 1
		} else {
			nnt = nonvarServeMuxNode
		}

		cn = cn.parent
		if cn != nil {
			switch nnt {
			case unmodifiedVarServeMuxNode:
				goto TryUnmodifiedVarNode
			case ellipsisModifiedVarServeMuxNode:
				goto TryEllipsisModifiedVarNode
			}
		} else if fnt == nonvarServeMuxNode {
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

// serveMuxNode is a node of the radix tree of a [ServeMux].
type serveMuxNode struct {
	prefix string
	label  byte
	typ    serveMuxNodeType
	parent *serveMuxNode

	nonvarChildren           []*serveMuxNode
	unmodifiedVarChild       *serveMuxNode
	ellipsisModifiedVarChild *serveMuxNode
	hasAtLeastOneChild       bool

	handlerTuples        map[string]*handlerTuple
	catchAllHandlerTuple *handlerTuple
	hasAtLeastOneHandler bool
}

// addChild adds the n as a child node to the mn.
func (mn *serveMuxNode) addChild(n *serveMuxNode) {
	switch n.typ {
	case nonvarServeMuxNode:
		mn.nonvarChildren[n.label] = n
	case unmodifiedVarServeMuxNode:
		mn.unmodifiedVarChild = n
	case ellipsisModifiedVarServeMuxNode:
		mn.ellipsisModifiedVarChild = n
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

// serveMuxNodeType is the type of a [serveMuxNode].
type serveMuxNodeType uint8

// The types of [serveMuxNode].
const (
	nonvarServeMuxNode serveMuxNodeType = iota
	unmodifiedVarServeMuxNode
	ellipsisModifiedVarServeMuxNode
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

// walkPath walks the given path and calls the f for each passed path element.
// If the f returns false, the walk stops.
func walkPath(path string, f func(recentlyPassedSlashes, elem string, elemIndex int) bool) {
	var recentlyPassedSlashes string
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			recentlyPassedSlashes += "/"
			continue
		}
		j := i
		for ; i < len(path) && path[i] != '/'; i++ {
		}
		elem := path[j:i]
		if !f(recentlyPassedSlashes, elem, j) {
			break
		}
		if i < len(path) {
			recentlyPassedSlashes = "/"
		}
	}
}
