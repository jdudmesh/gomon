import "./main.css";

import Alpine from "alpinejs";
import htmx from "htmx.org";

interface SSEEvent {
  id: string;
  dt: string;
  target: string;
  swap: string;
  markup: string;
}

export type SwapType =
  | "innerHTML"
  | "outerHTML"
  | "beforebegin"
  | "afterbegin"
  | "beforeend"
  | "afterend"
  | "delete"
  | "none";

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
  runId: "all",
  isShowingSearchResults: false,
  isShowingConnectionError: false,
  eventSource: new EventSource("/sse?stream=events", {
    withCredentials: false
  }),
  eventQueue: [] as MessageEvent[],
  toastTimeout: null as number | null,
  zoomContent: "",
  init: function () {
    console.log("init");
    this.$watch("searchText", (val) => {
      this.onSearchTextChanged(val);
    });
    this.$watch("runId", (val) => {
      this.onRunIdChanged(val);
    });
    this.$watch("isShowingSearchResults", (val) => {
      this.onIsShowingSearchResults(val);
    });

    this.eventSource.onmessage = (ev) => {
      this.handleEventSourceMessage(ev);
    };
    this.eventSource.onerror = () => {
      this.handleEventSourceError();
    };
  },
  onSearchTextChanged: function (value: string) {
    if (value.length === 0) {
      this.isShowingSearchResults = false;
      return;
    }
    this.isShowingSearchResults = true;
    this.onClickSearch();
  },
  onRunIdChanged: function (value: string) {
    if (value === "all") {
      this.isShowingSearchResults = false;
      this.searchText = "";
      return;
    }
    this.isShowingSearchResults = true;
    if (this.searchText.length > 0) {
      this.onClickSearch();
    }
  },
  onIsShowingSearchResults: function (value: boolean) {
    console.log("isShowingSearchResults changed", value);
    if (!value) {
      while (this.eventQueue.length > 0) {
        const ev = this.eventQueue.shift()!;
        this.processEvent(ev);
      }
    }
  },
  onSearchTextKeyDown: function (ev: KeyboardEvent) {
    if (ev.key === "Enter") {
      this.onClickSearch();
    }
  },
  onClickSearch: function () {
    const targetEl = document.querySelector("#log-output-inner") as HTMLElement;
    if (!targetEl) {
      throw new Error("Target element not found");
    }
    const event = new CustomEvent("custom:search", {
      detail: { key: "value" }
    });
    targetEl.dispatchEvent(event);
  },
  handleEventSourceMessage: function (ev: MessageEvent) {
    if (this.isShowingSearchResults) {
      this.eventQueue.push(ev);
      return;
    }
    this.processEvent(ev);
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
  },
  processEvent: function (ev: MessageEvent) {
    const msg = JSON.parse(ev.data) as SSEEvent;
    swap(msg);
  },
  onZoomEntry: function (ev: MouseEvent) {
    const targetEl = ev.target as HTMLElement;
    const textContent = targetEl
      .closest(".log-entry")
      ?.querySelector(".log-text")?.textContent;
    try {
      const data = JSON.parse(textContent || "");
      this.zoomContent = JSON.stringify(data, null, 2);
    } catch (e) {
      this.zoomContent = textContent || "";
    }
    const dialog = document.getElementById("zoom-dialog") as HTMLDialogElement;
    dialog.showModal();
  }
}));

Alpine.start();

function swap(msg: SSEEvent) {
  const targetEl = document.querySelector(msg.target) as HTMLElement;
  if (!targetEl) {
    throw new Error(`Target element not found: ${msg.target}/${msg.id}`);
  }

  const f = msg.swap.split(" ");
  const swapType = f[0] as SwapType;
  const scrollExpr = f.length > 1 ? f[1] : "";

  const range = document.createRange();
  range.selectNode(targetEl);
  const documentFragment = range.createContextualFragment(msg.markup);

  switch (swapType) {
    case "innerHTML":
      while (targetEl.firstChild) {
        targetEl.removeChild(targetEl.firstChild);
      }
      targetEl.appendChild(documentFragment);
      break;
    case "outerHTML":
      targetEl.parentNode?.replaceChild(documentFragment, targetEl);
      break;
    case "beforebegin":
      targetEl.parentNode?.insertBefore(documentFragment, targetEl);
      break;
    case "afterbegin":
      targetEl.insertBefore(documentFragment, targetEl.firstChild);
      break;
    case "beforeend":
      targetEl.appendChild(documentFragment);
      break;
    case "afterend":
      targetEl.parentNode?.insertBefore(documentFragment, targetEl.nextSibling);
      break;
    default:
      break;
  }

  if (scrollExpr) {
    const f = scrollExpr.split(":");
    const scrollType = f[0];
    const scrollTarget = f[1];
    switch (scrollType) {
      case "scroll":
        switch (scrollTarget) {
          case "view":
            (documentFragment.firstChild as HTMLElement)?.scrollIntoView();
            break;
          case "lastchild":
            (targetEl.lastChild as HTMLElement)?.scrollIntoView({
              block: "end",
              behavior: "instant"
            });
            break;
          case "nextsibling":
            (targetEl.nextSibling as HTMLElement)?.scrollIntoView({
              block: "end",
              behavior: "instant"
            });
            break;
        }
        break;
    }
  }
}