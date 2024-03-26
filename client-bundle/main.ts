import "./main.css";
import { kilo, actor, state } from "./lib/kilo";

kilo().baseUrl("http://localhost:4001");

const appState = state({
  searchText: "",
  runId: 0,
  isShowingSearchResults: false,
  isShowingConnectionError: false
})
  .bind("searchText", "#search-input")
  .bind("runId", "#search-select");

const searchSelectInitActor = actor("#search-select")
  .get("/components/search-select")
  .swap("outerHTML");

const logActor = actor("#log-output-inner")
  .get("/actions/search")
  .swap("innerHTML scroll:lastchild");

const searchFormActor = actor("#search-form")
  .get("/actions/search")
  .before(() => {
    appState.model.isShowingSearchResults = true;
  })
  .target("#log-output-inner");

appState.watch("searchText", (searchText) => {
  const pause = searchText.length > 0;
  if (!pause) {
    appState.model.isShowingSearchResults = false;
  }
  eventSource.pause(pause);
  const el = document.querySelector(".blinking-cursor") as HTMLElement;
  if (!el) return;
  el.style.visibility = pause ? "hidden" : "visible";
});

appState.watch("isShowingSearchResults", (isShowingSearchResults) => {
  if (!isShowingSearchResults) {
    eventSource.clear();
    searchSelectInitActor.retrigger();
    logActor.retrigger();
  }
});

appState.watch("isShowingConnectionError", (isShowingConnectionError) => {
  document.getElementById("connection-error")!.style.display =
    isShowingConnectionError ? "block" : "none";
});

appState.watch("runId", (runId) => {
  const el = document.querySelector("#search-select") as HTMLSelectElement;
  const currentRunId = el.getAttribute("data-current-run-id") as string;
  const pause = runId != currentRunId;
  eventSource.pause(pause);
  if (pause && appState.model.searchText.length > 0) {
    searchFormActor.retrigger();
  }
  const cursorEl = document.querySelector(".blinking-cursor") as HTMLElement;
  if (!cursorEl) return;
  cursorEl.style.visibility = pause ? "hidden" : "visible";
});

actor("#restart").post("/actions/restart").swap("none");

actor("#exit").post("/actions/exit").swap("none");

let toastTimeout: number | null = null;
const eventSource = kilo()
  .sse("/sse?stream=events", {
    withCredentials: false
  })
  .onError((ev) => {
    console.log("sse error");
    appState.model.isShowingConnectionError = true;
    if (toastTimeout) {
      clearTimeout(toastTimeout);
    }
    toastTimeout = setTimeout(() => {
      appState.model.isShowingConnectionError = false;
    }, 5000);
  });

  function onClickEntry(ev: Event) {
    const el = ev.target as HTMLElement;
    console.log(el);
  }

  actor(".entry-button").on("click").do(onClickEntry);