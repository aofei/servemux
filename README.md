# ServeMux

An EXPERIMENTAL prototype for https://github.com/golang/go/discussions/60227.

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
