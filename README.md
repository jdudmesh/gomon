# Overview
`gomon` is a tool to monitor and hot reload go programs. The DX for many front end frameworks is very good. Programs reload when file changes are detected and, for web apps, browsers are automatically reloaded. Commonly tools like `nodemon` and `Vite` are used to achieve this.

The aim is to provide a similar experience to these tools.

There was a previous [approach to this problem](https://github.com/jdudmesh/hotreload-go) however it required instrumenting your existing code and was not able to restart on `.go` file changes. This approach instead provides a go tool which can run your go program and restart as required.

# Usage

## Installation
Install the tool as follows:
```bash
go install https://github.com/jdudmesh/gomon@latest
```

## Basic Usage
In your project directory run:

```bash
gomon <path to main.go>
```

`gomon` supports a number of command line parameters:
```
--root     - use an alternative root directory
--template - specify a glob pattern to watch for HTML template changes (see below)
--env      - a comma separated list of environment variable files to load e.g. .env,.env.local

```

## Template files
If your project contains Go HTML templates then you can reload them by using the `--template` option above. This will send a `USR1` signal to your process which can trigger the reload.

For example:
```go
type Template struct {
templates *template.Template
}

func (t *Template) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
  return t.templates.ExecuteTemplate(w, name, data)
}

func main() {
  e := echo.New()
  e.Static("/assets", "./static")

  t := &Template{
    templates: template.Must(template.ParseGlob("views/*.html")),
  }
  e.Renderer = t

  quit := make(chan bool)
  go func() {
    sigint := make(chan os.Signal, 1)
    signal.Notify(sigint, syscall.SIGUSR1)
    for {
      select {
      case <-sigint:
        fmt.Println("Reloading templates...")
        t.templates = template.Must(template.ParseGlob("views/*.html"))
      case <-quit:
        return
      }
    }
  }()

  e.GET("/", func(c echo.Context) error {
    return c.Render(http.StatusOK, "index.html", nil)
  })

  if p, ok := os.LookupEnv("PORT"); ok {
    e.Logger.Fatal(e.Start(":" + p))
  } else {
    e.Logger.Fatal(e.Start(":8080"))
  }
  quit <- true
}
```

The template loader is also provided as a helper class:
```go

import (
  ...

  "github.com/jdudmesh/gomon/pkg/templates"
)

func main() {
  e := echo.New()
  e.Static("/assets", "./static")

  t, closeFn := templates.NewEcho("views/*.html")
  defer closeFn()
  e.Renderer = t

  e.GET("/", func(c echo.Context) error {
    return c.Render(http.StatusOK, "index.html", nil)
  })

  if p, ok := os.LookupEnv("PORT"); ok {
    e.Logger.Fatal(e.Start(":" + p))
  } else {
    e.Logger.Fatal(e.Start(":8080"))
  }

  quit <- true
}
```

