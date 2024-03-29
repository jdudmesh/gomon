# Overview

`gomon` is a tool to monitor and hot reload go programs. The DX for many front end frameworks like NextJS is very good. Changing a Programs reload when file changes are detected and, for web apps, browsers are automatically reloaded. Commonly tools like `nodemon` and `Vite` are used to achieve this.

The aim is to provide a similar experience to these tools for Go programs.

For example usage see [this example](https://github.com/jdudmesh/gomon-example)

## Key features

- `go run` a project and force hard restart based on file changes defined by a list of file extensions (typically `*.go`)
- if the process fails to start then it is restarted using an exponential backoff strategy for up to 1 minute
- alternatively specify a different initial command
- perform a soft restart (e.g. reload templates) based on a file changes defined by second list of file extensions (typically `*.html`)
- ignore file changes in specified directories (e.g. `vendor`)
- load environment variables from e.g. `.env` files
- run scripts for generated files based on globs e.g. \*.templ
- Proxy http requests to the downstream project and automatically inject an HMR script
- Fire a page reload in the browser on hard or soft restart using SSE
- Implements a Web UI which displays and can search console logs with history
- prestart - run a list of tasks before running the main entrypoint e.g. `go generate`
- proxy only - if you're running your project in a debugger you can run the proxy only so that downstream proxies (e.g. caddy) aren't broken

## UI Screenshot

![UI Screenshot](https://github.com/jdudmesh/gomon/blob/main/screenshot/screenshot.png?raw=true)

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

```bash
--conf       - specify a config file (see below)
--dir        - use an alternative root directory
--env        - a comma separated list of environment variable files to load e.g. .env,.env.local
--proxy-only - don't start the child process, just run the proxy
```

## Working Directory

The working directory for `gomon` is the current directory unless:

1. if a root directory is specified then that is used
2. otherwise, if specified in the config file that is used
3. otherwise, the current directory is used

## Config files

If a config file is specified, or one is found in the working directory, then that is used. Command line flags override config file values.

The config file is a YAML file as follows:

```yaml
command: <optional array for command to run instead of `["go", "run"]`>
entrypoint:
entrypointArgs:
templatePathGlob: <relative path + glob to template directory>

envFiles: # changes to env files always trigger a hard reload
  - <env file e.g. .env>
  - ...

reloadOnUnhandled: true|false #if true then any file changes (not just .go files) will restart process

rootDirectory: <path to root>
entrypoint: <relative path to entry point>
entrypointArgs: [<array of args>]

excludePaths: [<array of relative paths to exlude from watch>]
hardReload: [<array of glob patterns to force hard reload>]
softReload: [<array of glob patterns to force soft reload>]

prestart: # these tasks will always run before `go run <entrypoint>` e.g. `go generate`
    - <list tasks to run>

generated:
  <glob pattern>:
    - <list tasks to run>
    - "__soft_reload" | "__hard_reload" #trigger manual reload on completion

envFiles:
  - <environment variable files to load>
reloadOnUnhandled: true|false # cold reload by default if file not otherwise handled
proxy:
  enabled: true # start a proxy server to inject HMR script
  port: <port num>
  downstream:
    host: <the host:port of your project> # e.g. localhost:8081
    timeout: <timeout in seconds> # downstream request timeout
ui:
  enabled: true
	port: 4001
```

## Web UI
`gomon` now supports a Web UI which displays captured console output. The aim is to make this fully searchable and to pretty print JSON logs where possible.

To enable ass the `ui` key to the config and set `enabled` to `true`. By default the UI listens on port 4001 but you can change it in the config. All log events are stored in a SQLITE database in a `.gomon` folder in the target project. This means that the output of previous runs of the code persists and can be searched. Don't forget to put `.gomon` in your `.gitignore` file.


## Template files
If your project contains Go HTML templates then you can reload them by defining them in the config file using the softReload property. `gomon` uses IPC to trigger a reload and wait for confirmation before triggering a hot reload in the downstream browsers. The project must make use of the [the `gomon` client](https://github.com/jdudmesh/gomon-client).

For example:
```go
package main

import (
	"net/http"

	client "github.com/jdudmesh/gomon-client"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	log.Info("starting server")

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	t, err := client.NewEcho("./views/*html")
	if err != nil {
		log.Fatal(err)
	}
	defer t.Close()

	go func() {
		err := t.ListenAndServe()
		if err != nil {
			log.Error(err)
		}
	}()

	e.Renderer = t

	e.GET("/", func(c echo.Context) error {
		return c.Render(http.StatusOK, "index.html", "World")
	})

	e.Logger.Fatal(e.Start(":8080"))
}
```

At the moment on a generic reloader and Labstack Echo are supported. Please raise an issue if you would like other support added for other frameworks.
