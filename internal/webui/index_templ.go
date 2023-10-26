// Code generated by templ@v0.2.364 DO NOT EDIT.

package webui

//lint:file-ignore SA4006 This context is only used if a nested component is present.

import "github.com/a-h/templ"
import "context"
import "io"
import "bytes"

// gomon is a simple command line tool that watches your files and automatically restarts the application when it detects any changes in the working directory.
// Copyright (C) 2023 John Dudmesh

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

import (
	"github.com/jdudmesh/gomon/internal/console"
	"strconv"
)

func Event(ev *console.LogEvent) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) (err error) {
		templBuffer, templIsBuffer := w.(*bytes.Buffer)
		if !templIsBuffer {
			templBuffer = templ.GetBuffer()
			defer templ.ReleaseBuffer(templBuffer)
		}
		ctx = templ.InitializeContext(ctx)
		var_1 := templ.GetChildren(ctx)
		if var_1 == nil {
			var_1 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		_, err = templBuffer.WriteString("<div class=\"flex flex-row text-blue-400 font-mono\" data-event-date=\"{ev.CreatedAt}\"><div class=\"w-1/6\">")
		if err != nil {
			return err
		}
		var var_2 string = ev.CreatedAt.Format("15:04:05.000")
		_, err = templBuffer.WriteString(templ.EscapeString(var_2))
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</div><div class=\"w-5/6 break-all\">")
		if err != nil {
			return err
		}
		var var_3 string = ev.EventData
		_, err = templBuffer.WriteString(templ.EscapeString(var_3))
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</div></div>")
		if err != nil {
			return err
		}
		if !templIsBuffer {
			_, err = templBuffer.WriteTo(w)
		}
		return err
	})
}

func EventList(events []*console.LogEvent) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) (err error) {
		templBuffer, templIsBuffer := w.(*bytes.Buffer)
		if !templIsBuffer {
			templBuffer = templ.GetBuffer()
			defer templ.ReleaseBuffer(templBuffer)
		}
		ctx = templ.InitializeContext(ctx)
		var_4 := templ.GetChildren(ctx)
		if var_4 == nil {
			var_4 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		for _, ev := range events {
			_, err = templBuffer.WriteString("<div class=\"flex flex-row text-blue-400 font-mono\" data-event-date=\"{ev.CreatedAt}\"><div class=\"w-1/6\">")
			if err != nil {
				return err
			}
			var var_5 string = ev.CreatedAt.Format("15:04:05.000")
			_, err = templBuffer.WriteString(templ.EscapeString(var_5))
			if err != nil {
				return err
			}
			_, err = templBuffer.WriteString("</div><div class=\"w-5/6 break-all\">")
			if err != nil {
				return err
			}
			var var_6 string = ev.EventData
			_, err = templBuffer.WriteString(templ.EscapeString(var_6))
			if err != nil {
				return err
			}
			_, err = templBuffer.WriteString("</div></div>")
			if err != nil {
				return err
			}
		}
		if !templIsBuffer {
			_, err = templBuffer.WriteTo(w)
		}
		return err
	})
}

func Console(currentRun int, runs []*console.LogRun, events []*console.LogEvent) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) (err error) {
		templBuffer, templIsBuffer := w.(*bytes.Buffer)
		if !templIsBuffer {
			templBuffer = templ.GetBuffer()
			defer templ.ReleaseBuffer(templBuffer)
		}
		ctx = templ.InitializeContext(ctx)
		var_7 := templ.GetChildren(ctx)
		if var_7 == nil {
			var_7 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		_, err = templBuffer.WriteString("<!doctype html><html><head><title>")
		if err != nil {
			return err
		}
		var_8 := `gomon console`
		_, err = templBuffer.WriteString(var_8)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</title><script src=\"https://unpkg.com/htmx.org@1.9.6\" integrity=\"sha384-FhXw7b6AlE/jyjlZH5iHa/tTe9EpJ1Y55RjcgPbjeWMskSxZt1v9qkxLJWNJaGni\" crossorigin=\"anonymous\">")
		if err != nil {
			return err
		}
		var_9 := ``
		_, err = templBuffer.WriteString(var_9)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</script><script src=\"https://cdn.tailwindcss.com\">")
		if err != nil {
			return err
		}
		var_10 := ``
		_, err = templBuffer.WriteString(var_10)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</script><link href=\"https://cdn.jsdelivr.net/npm/daisyui@3.9.3/dist/full.css\" rel=\"stylesheet\" type=\"text/css\"><style>")
		if err != nil {
			return err
		}
		var_11 := `#event-list > :first-child { margin-top: auto !important; }`
		_, err = templBuffer.WriteString(var_11)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</style></head><body class=\"bg-slate-900 text-white flex flex-col h-screen\" data-current-run=\"")
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString(templ.EscapeString(strconv.Itoa(currentRun)))
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("\"><nav class=\"grow-0 flex flex-row mx-2 p-2 justify-between items-center\"><div class=\"flex flex-row\"><a href=\"/\" class=\"text-2xl text-bold\">")
		if err != nil {
			return err
		}
		var_12 := `gomon`
		_, err = templBuffer.WriteString(var_12)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</a></div><div class=\"flex flex-row items-center gap-2\"><label class=\"px-4\">")
		if err != nil {
			return err
		}
		var_13 := `Filter:`
		_, err = templBuffer.WriteString(var_13)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</label><input type=\"text\" name=\"filter\" class=\"input input-bordered text-slate-900\" placeholder=\"filter\" data-send=\"true\"><label class=\"px-4\">")
		if err != nil {
			return err
		}
		var_14 := `Stream:`
		_, err = templBuffer.WriteString(var_14)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</label><select name=\"stm\" class=\"select select-bordered text-slate-900\" hx-get=\"/search\" hx-include=\"[data-send=&#39;true&#39;]\" hx-target=\"#event-list\" hx-trigger=\"input\" data-send=\"true\"><option value=\"all\" selected>")
		if err != nil {
			return err
		}
		var_15 := `all`
		_, err = templBuffer.WriteString(var_15)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</option><option value=\"stdout\">")
		if err != nil {
			return err
		}
		var_16 := `stdout`
		_, err = templBuffer.WriteString(var_16)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</option><option value=\"stderr\">")
		if err != nil {
			return err
		}
		var_17 := `stderr`
		_, err = templBuffer.WriteString(var_17)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</option></select><label class=\"px-4\">")
		if err != nil {
			return err
		}
		var_18 := `Run:`
		_, err = templBuffer.WriteString(var_18)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</label><select name=\"run\" class=\"select select-bordered text-slate-900\" hx-get=\"/search\" hx-include=\"[data-send=&#39;true&#39;]\" hx-target=\"#event-list\" hx-trigger=\"input\" data-send=\"true\">")
		if err != nil {
			return err
		}
		for _, r := range runs {
			_, err = templBuffer.WriteString("<option value=\"")
			if err != nil {
				return err
			}
			_, err = templBuffer.WriteString(templ.EscapeString(strconv.Itoa(r.ID)))
			if err != nil {
				return err
			}
			_, err = templBuffer.WriteString("\"")
			if err != nil {
				return err
			}
			if int(r.ID) == currentRun {
				if true {
					_, err = templBuffer.WriteString(" selected")
					if err != nil {
						return err
					}
				}
			}
			_, err = templBuffer.WriteString(">")
			if err != nil {
				return err
			}
			var var_19 string = r.CreatedAt.Format("2006-01-02 15:04:05")
			_, err = templBuffer.WriteString(templ.EscapeString(var_19))
			if err != nil {
				return err
			}
			_, err = templBuffer.WriteString("</option>")
			if err != nil {
				return err
			}
		}
		_, err = templBuffer.WriteString("</select><button id=\"btn-search\" class=\"btn btn-primary\" hx-get=\"/search\" hx-include=\"[data-send=&#39;true&#39;]\" hx-target=\"#event-list\" hx-trigger=\"click\">")
		if err != nil {
			return err
		}
		var_20 := `Search`
		_, err = templBuffer.WriteString(var_20)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</button><button id=\"btn-restart\" class=\"btn btn-secondary\" hx-post=\"/restart\">")
		if err != nil {
			return err
		}
		var_21 := `Hard Restart`
		_, err = templBuffer.WriteString(var_21)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</button></div></nav><main id=\"event-list\" class=\"grow mx-4 my-2 p-4 border-solid border border-blue-400 rounded-lg flex flex-col overflow-y-auto\">")
		if err != nil {
			return err
		}
		err = EventList(events).Render(ctx, templBuffer)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</main><script>")
		if err != nil {
			return err
		}
		var_22 := `
				const currentRun = document.body.getAttribute("data-current-run");
				const eventList = document.getElementById("event-list");
				function listen() {
					const logSource = new EventSource("/__gomon__/events?stream=logs", {
						withCredentials: true,
					});

					const runSource = new EventSource("/__gomon__/events?stream=runs", {
						withCredentials: true,
					});

					logSource.onmessage = (event) => {
						const selectedRun = document.querySelector("select[name=run]").value;
						if (selectedRun != currentRun) {
							return;
						}
						eventList.insertAdjacentHTML("beforeend", event.data);
						eventList.scrollTop = eventList.scrollHeight;
					};

					runSource.onmessage = (event) => {
						window.location.reload();
					};
				}

				function clearConsole() {
					eventList.innerHTML = "";
				}

				listen();
			`
		_, err = templBuffer.WriteString(var_22)
		if err != nil {
			return err
		}
		_, err = templBuffer.WriteString("</script></body></html>")
		if err != nil {
			return err
		}
		if !templIsBuffer {
			_, err = templBuffer.WriteTo(w)
		}
		return err
	})
}