import "./main.css";

import Alpine from "alpinejs";
import htmx from "htmx.org";

interface SSEEvent {
  target: string;
  swap: string;
  markup: string;
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
    const targetEl = document.querySelector(msg.target) as HTMLElement;
    if (!targetEl) {
      throw new Error("Target element not found");
    }

    const range = document.createRange();
    range.selectNode(targetEl);
    const documentFragment = range.createContextualFragment(msg.markup);

    if (msg.target === "#log-output-inner") {
      targetEl.appendChild(documentFragment);

      (targetEl.lastChild as HTMLElement)?.scrollIntoView({
        block: "end",
        behavior: "instant"
      });
    }

    if (msg.target === "#search-select") {
      targetEl.parentNode?.replaceChild(documentFragment, targetEl);
    }
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
