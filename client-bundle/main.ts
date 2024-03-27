import "./main.css";

import Alpine from "alpinejs";
import htmx from "htmx.org";

interface SSEEvent {
	target: string
	swap:   string
	markup: string
}

declare global {
  interface Window {
    Alpine: typeof Alpine;
    htmx: typeof htmx;
  }
}

window.Alpine = Alpine;
window.htmx = htmx;

Alpine.data("search", () => ({
  searchText: "",
  runId: 0,
  isPaused: false,
  isShowingSearchResults: false,
  isShowingConnectionError: false,
  eventSource: new EventSource("/sse?stream=events", {withCredentials: false}),
  eventQueue: [] as MessageEvent[],
  toastTimeout: null as number | null,
  init: function () {
    console.log("init");
    this.$watch("searchText", this.onSearchTextChanged);

    this.eventSource.onmessage = (ev) => {
      this.handleEventSourceMessage(ev);
    }
    this.eventSource.onerror = () => {
      this.handleEventSourceError();
    }
  },
  onSearchTextChanged: function (value: string) {
    console.log("search text changed", value);
  },
  handleEventSourceMessage: function (ev: MessageEvent) {
    if (this.isPaused) {
      this.eventQueue.push(ev);
      return;
    }

    const msg = JSON.parse(ev.data) as SSEEvent;
    const targetEl = document.querySelector(msg.target) as HTMLElement;
    if (!targetEl) {
      throw new Error("Target element not found");
    }

    const range = document.createRange();
    range.selectNode(targetEl);
    const documentFragment = range.createContextualFragment(msg.markup);

    if(msg.target === "#log-output-inner") {
      targetEl.appendChild(documentFragment);

      (targetEl.lastChild as HTMLElement)?.scrollIntoView({
        block: "end",
        behavior: "instant"
      });
    }

    if(msg.target === "#search-select") {
      targetEl.parentNode?.replaceChild(documentFragment, targetEl);
    }
  },
  handleEventSourceError: function () {
    console.log("sse error");
    this.isShowingConnectionError = true;
    if (this.toastTimeout) {
      clearTimeout(this.toastTimeout);
    }
    this.toastTimeout = setTimeout(() => {
      this.isShowingConnectionError = false;
    }, 5000);
  }
}))

Alpine.start()




// appState.watch("searchText", (searchText) => {
//   const pause = searchText.length > 0;
//   if (!pause) {
//     appState.model.isShowingSearchResults = false;
//   }
//   eventSource.pause(pause);
//   const el = document.querySelector(".blinking-cursor") as HTMLElement;
//   if (!el) return;
//   el.style.visibility = pause ? "hidden" : "visible";
// });

// appState.watch("isShowingSearchResults", (isShowingSearchResults) => {
//   if (!isShowingSearchResults) {
//     eventSource.clear();
//     searchSelectInitActor.retrigger();
//     logActor.retrigger();
//   }
// });

// appState.watch("isShowingConnectionError", (isShowingConnectionError) => {
//   document.getElementById("connection-error")!.style.display =
//     isShowingConnectionError ? "block" : "none";
// });

// appState.watch("runId", (runId) => {
//   const el = document.querySelector("#search-select") as HTMLSelectElement;
//   const currentRunId = el.getAttribute("data-current-run-id") as string;
//   const pause = runId != currentRunId;
//   eventSource.pause(pause);
//   if (pause && appState.model.searchText.length > 0) {
//     searchFormActor.retrigger();
//   }
//   const cursorEl = document.querySelector(".blinking-cursor") as HTMLElement;
//   if (!cursorEl) return;
//   cursorEl.style.visibility = pause ? "hidden" : "visible";
// });
