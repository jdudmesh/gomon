```bash
go intall https://github.com/jdudmesh/gomon
```

# Overview
`gomon` is a tool to monitor and hot reload go programs. The DX for many front end frameworks is very good. Programs reload when file changes are detected and, for web apps, browsers are automatically reloaded. Commonly tools like `nodemon` and `Vite` are used to achieve this.

The aim is to provide a similar experience to these tools.

There was a previous [approach to this problem](https://github.com/jdudmesh/hotreload-go) however it required instrumenting your existing code and was not able to restart on `.go` file changes. This approach instead provides a go tool which can run your go program and restart as required.

For example usage see [this example](https://github.com/jdudmesh/gomon-example)

## Key features
* `go run` a project and force hard restart based on file changes defined by a list of file extensions (typically `*.go`)
* load environment variables from e.g. `.env` files
* perform a soft restart (e.g. reload templates) based on a file changes defined by second list of file extensions (typically `*.html`)
* ignore file changes in specified directories (e.g. `vendor`)
* Proxy http requests to the downstream project and automatically inject an HMR script
* Fire a page reload in the browser on hard or soft restart using SSE

# Usage

## Installation
Install the tool as follows:
```bash
go install github.com/jdudmesh/gomon@latest
```

## Basic Usage
In your project directory run:

```bash
gomon <path to main.go>
```

This will simply `go run` your project and restart on changes to `*.go` files.

`gomon` supports a number of command line parameters:
```
--config   - specify a config file (see below)
--directory     - use an alternative root directory
--env      - a comma separated list of environment variable files to load e.g. .env,.env.local

```
## Working Directory
The working directory for `gomon` is the current directory unless:
1. if a root directory is specified then that is used
2. otherwise, if a config file is specified then the directory containing the file is used
3. otherwise, if specified in the config file that is used
3. otherwise, the current directory is used

## Config files
If a config file is specified, or one is found in the working directory, then that is used. Command line flags override config file values.

The config file is a YAML file as follows:
```yaml

entrypoint:
entrypointArgs:
templatePathGlob: <relative path + glob to template directory>
envFiles:
  - <env file e.g. .env>
  - ...
reloadOnUnhandled: true|false #if true then any file changes (not just .go files) will restart process

rootDirectory: <path to root>
entrypoint: <relative path to entry point>
entrypointArgs: [<array of args>]
hardReload: [<array of glob patterns to force hard reload>]
softReload: [<array of glob patterns to force soft reload>]
excludePaths: [<array of relative paths to exlude from watch>]

envFiles:
  - <environment variable files to load>
reloadOnUnhandled: true|false # cold reload by default if file not otherwise handled
proxy:
  enabled: true # start a proxy server to inject HMR script
  port: <port num>
  downstream:
    host: <the host:port of your project> # e.g. localhost:8081
    timeout: <timeout in seconds> # downstream request timeout

```

## Template files
If your project contains Go HTML templates then you can reload them by defining them in the config file using the softReload property. `gomon` uses IPC to trigger a reload and wait for confirmation before triggering a hot reload in the downstream browsers. The project must make use of the [the `gomon` client](https://github.com/jdudmesh/gomon-client).

For example:
```go
import (
	"fmt"
	"net/http"
	"os"

	templates "github.com/jdudmesh/gomon/pkg/client"
	"github.com/labstack/echo/v4"
)

func main() {
	e := echo.New()
	e.Static("/assets", "./static")

	t, err := templates.NewEcho("views/*.html", e.Logger)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer t.Close()
	if err := t.Run(); err != nil {
		panic(err)
	}

	e.Renderer = t

	e.GET("/", func(c echo.Context) error {
		return c.Render(http.StatusOK, "index.html", nil)
	})

	if p, ok := os.LookupEnv("PORT"); ok {
		e.Logger.Fatal(e.Start(":" + p))
	} else {
		e.Logger.Fatal(e.Start(":8080"))
	}
}
```

At the moment on a generic reloader and Labstack Echo are supported. Please raise an issue if you would like other support added for other frameworks.