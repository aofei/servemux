# ServeMux

An EXPERIMENTAL prototype for https://github.com/golang/go/discussions/60227#discussioncomment-6005976.

## Usage

```go
package main

import (
	"fmt"
	"net/http"

	"github.com/aofei/servemux"
)

func main() {
	mux := servemux.NewServeMux()
	mux.HandleFunc("/hello/{name}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, %s\n", servemux.PathVars(r)["name"])
	})
	http.ListenAndServe("localhost:8080", mux)
}
```

## Pattern Rules

This section declares rules that apply to individual patterns:

1. A pattern must be in the form of `[method ][host][path]`, where at least one of the host and path must be present, while the method is always optional.
2. A method must match `^[0-9A-Za-z]+$` (at least one alphanumeric character).
3. A host must be able to be parsed using `net/url.Parse("http://" + host + "/")`.
4. A path must be in the form of `/[path-elements/]`, where each path element must either be a variable (starting with `{` and ending with `}`) or not.
5. A non-variable path element must match `^[^/]+$` (at least one character and that character is not `/`).
6. A variable path element must be in the form of `{[name][modifier]}`, where both the name and modifier are optional.
7. The name of a variable path element must match `^[_\pL][_\pL\p{Nd}]*$` (a Go identifier).
8. All variable path elements within the same path must have unique names.
9. The modifier of a variable path element can only be `...` or `$`.
10. A variable modified by `...` or `$` can only be the last path element.
11. A `$`-modified variable path element must have no name.

## Pattern Registration

This section describes what happens when registering patterns, which occurs when calling `ServeMux.Handle`:

1. A pattern with a host will be registered in the dedicated tree for that host, while a pattern without a host will be registered in the hostless tree.
2. A pattern whose path ends with `/` is equivalent to that path concatenated with `{...}` at the end. E.g., the pattern `/` is equivalent to `/{...}`, and the pattern `/subtree/` is equivalent to `/subtree/{...}`.
3. A pattern whose path starts with only non-variable path elements and ends with either `/` or `/{[name]...}` will result in a special pattern being registered internally. This special pattern is essentially identical to the original pattern, except that its method and the trailing `/` or `/{[name]...}` in its path are removed. The handler for this special pattern will be an internally-generated handler that redirects to the root of the last path element in the original pattern. This behavior can be overridden with a separate registration for the path without the trailing `/` or `/{[name]...}`. E.g., when registering the pattern `/subtree/`, the pattern `/subtree` will be registered internally with an internally-generated handler that redirects to `/subtree/`, unless the pattern `/subtree` has been registered separately.
4. Two patterns that differ only in the names of the variable path elements are considered identical and will result in registration failure. E.g., the pattern `/foo/{bar}` is considered identical to `/foo/{baz}`, but it is not identical to `/foo/{bar...}`.
5. A registration failure will result in a panic.

## Request Matching

This section describes what happens when matching requests, which occurs when calling `ServeMux.Handler`:

1. The request host and path are sanitized before matching, except when the request method is `CONNECT`. If the request host contains a port, the port will be ignored during matching. If the request path is not in its canonical form, the matched handler will be replaced with an internally-generated handler that redirects to the canonical path.
2. When matching a request, the host is matched first. If a dedicated tree for that host is found, the match continues in that tree. If the match fails or there is no dedicated tree for that host, the match continues in the hostless tree.
3. After matching a request host, the next step is to match the request path. When matching a request path, path elements always follow the following precedence: non-variable > `$`-modified variable > unmodified variable > `...`-modified variable.
4. A non-variable path element matches characters verbatim. E.g., the pattern `/foo/bar` will only match the request path `/foo/bar`.
5. A `$`-modified variable path element (`{$}`) stops the variable from matching anything. E.g., the pattern `/foo/{$}` will only match the request path `/foo/`.
6. An unmodified variable path element (`{[name]}`) matches all characters except `/`. E.g., the pattern `/foo/{bar}` will match a request path like `/foo/` and `/foo/bar`, but it will not match a request path like `/foo` or `/foo/bar/`.
7. A `...`-modified variable path element (`{[name]...}`) greedily matches all characters, including `/`. E.g., the pattern `/foo/{bar...}` will match a request path like `/foo/`, `/foo/bar`, and `/foo/bar/`. Additionally, for a request path like `/foo`, there may be a special matching case described in item 3 of the "Pattern Registration" section.
8. After matching a request path, the next step is to match the request method. When matching a request method, the first thing is to find a handler for that method. If it is found, the match ends successfully. If it is not found, and there is no handler available for any other method, but a handler with no specified method is found, the match also ends successfully. Otherwise, when there are handlers for other methods, the match fails with an internally-generated handler responds status `405 (Method Not Allowed)`. If no handler is available at all, the match fails with an internally-generated handler responds status `404 (Not Found)`.
9. All variable path element values are resolved upon matching. For unnamed variable path elements, their values will be silently dropped.
