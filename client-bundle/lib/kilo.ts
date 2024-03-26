// import { watch, watchEffect } from "@vue-reactivity/watch";
import { watch } from "@vue-reactivity/watch";
import { reactive, type UnwrapNestedRefs } from "@vue/reactivity";
import type {
  ActorRegistry,
  StateRegistry,
  BindingEntry,
  SSEEventSource,
  EventHandler,
  KiloDef,
  SwapType,
  Path,
  RequestConfig,
  RequestConfigFn,
  PostRequestFn,
  ActorContext,
  StateContext,
  Base,
  SSE,
  Swapper,
  Target,
  Actor,
  Trigger,
  State,
  Model,
  RetriggerableActor,
  Retrigger,
  SwappableTarget
} from "./types";

let baseUrl = "";

const actorRegistry = reactive({
  state: "loading",
  contexts: [],
  sseSources: []
} as ActorRegistry);

const stateRegistry: StateRegistry = {
  contexts: []
};

watch(
  () => actorRegistry.contexts,
  (contexts, prev) => {
    if (
      !(
        actorRegistry.state === "interactive" ||
        actorRegistry.state === "complete"
      )
    ) {
      return;
    }
    for (const ctx of contexts) {
      if (prev.includes(ctx)) {
        continue;
      }
      const src = document.querySelector(ctx.sourceSelector) as HTMLElement;
      if (src) {
        src.dispatchEvent(new CustomEvent("kilo:load"));
      }
    }
  }
);

const stateBindingObserver = new MutationObserver((mutations) => {
  for (const m of mutations) {
    for (const removed of m.removedNodes) {
      if (removed.nodeType !== Node.ELEMENT_NODE) {
        continue;
      }
      const el = removed as HTMLElement;
      for (const ctx of stateRegistry.contexts) {
        if (ctx.bindings) {
          for (const key in ctx.bindings) {
            if (ctx.bindings[key].element === el) {
              for (const event of ctx.bindings[key].events) {
                ctx.bindings[key].element.removeEventListener(
                  "change",
                  ctx.bindings[key].handler
                );
              }
              const fn = _binder(ctx);
              fn(key, ctx.bindings[key].selector);
            }
          }
        }
      }
    }
  }
});

document.addEventListener("readystatechange", (ev: Event) => {
  actorRegistry.state = document.readyState;
  if (
    document.readyState === "interactive" ||
    actorRegistry.state === "complete"
  ) {
    stateBindingObserver.observe(document.body, {
      childList: true,
      subtree: true
    });

    for (const ctx of actorRegistry.contexts) {
      const src = document.querySelector(ctx.sourceSelector) as HTMLElement;
      if (!src) {
        continue;
      }
      src.dispatchEvent(new CustomEvent("kilo:load"));
    }
    for (const src of actorRegistry.sseSources) {
      if (!src.messageHandler) {
        continue;
      }
      src.source.addEventListener("message", src.messageHandler);
    }
  }
});

function defaultSSEEventHandler(src: SSEEventSource): EventHandler {
  return (ev: Event) => {
    try {
      const e = ev as MessageEvent;
      if (src.isPaused) {
        src.queue.push(e);
        return;
      }
      const msg = JSON.parse(e.data) as KiloDef;
      const target = document.querySelector(
        msg["x-kilo-target"]
      ) as HTMLElement;
      const swap = msg["x-kilo-swap"];
      const markup = msg["x-kilo-markup"];
      if (!target) {
        throw new Error("Target element not found");
      }
      _swap(
        {
          sourceSelector: msg["x-kilo-target"],
          targetSelector: msg["x-kilo-target"],
          triggerEvent: null,
          trigger: async () => {},
          actor: null,
          swapper: null,
          beforeActor: null,
          afterActor: null
        },
        swap,
        markup
      );
    } catch (e) {
      console.error(e);
      console.log(JSON.parse((ev as MessageEvent).data));
    }
  };
}

async function _swap(ctx: ActorContext, swapExpr: string, markup: string) {
  const src = document.querySelector(ctx.sourceSelector) as HTMLElement;
  if (!src) {
    throw new Error("source element not found");
  }
  let target = src;
  if (ctx.targetSelector) {
    target = document.querySelector(ctx.targetSelector) as HTMLElement;
    if (!target) {
      throw new Error("target element not found");
    }
  }
  const f = swapExpr.split(" ");
  const swapType = f[0] as SwapType;
  const scrollExpr = f.length > 1 ? f[1] : "";

  const range = document.createRange();
  range.selectNode(target);
  const documentFragment = range.createContextualFragment(markup);
  switch (swapType) {
    case "innerHTML":
      while (target.firstChild) {
        target.removeChild(target.firstChild);
      }
      target.appendChild(documentFragment);
      break;
    case "outerHTML":
      target.parentNode?.replaceChild(documentFragment, target);
      break;
    case "beforebegin":
      target.parentNode?.insertBefore(documentFragment, target);
      break;
    case "afterbegin":
      target.insertBefore(documentFragment, target.firstChild);
      break;
    case "beforeend":
      target.appendChild(documentFragment);
      break;
    case "afterend":
      target.parentNode?.insertBefore(documentFragment, target.nextSibling);
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
            (target.lastChild as HTMLElement)?.scrollIntoView({
              block: "end",
              behavior: "instant"
            });
            break;
          case "nextsibling":
            (target.nextSibling as HTMLElement)?.scrollIntoView({
              block: "end",
              behavior: "instant"
            });
            break;
        }
        break;
    }
  }
}

function extractFormData(form: HTMLFormElement, config: RequestConfig) {
  const formData = new FormData(form);
  switch (config.method) {
    case "GET":
      config.url =
        config.url + "?" + new URLSearchParams(formData as any).toString();
      break;
    case "POST":
      // TODO: support other content types (multipart/form-data, application/json, etc.)
      config.body = formData;
      break;
  }
}

async function _doRequest(ctx: ActorContext, url: Path, method: string) {
  const src = document.querySelector(ctx.sourceSelector) as HTMLElement;
  if (!src) {
    throw new Error("source element not found");
  }

  const requestUrl = typeof url === "string" ? url : url(ctx);
  const params = new FormData();
  const config: RequestConfig = {
    url: baseUrl + requestUrl,
    contentType: "",
    cancel: false,
    method: method
  };

  if (src.tagName === "FORM") {
    extractFormData(src as HTMLFormElement, config);
  }

  if (ctx.beforeActor) {
    await ctx.beforeActor(config);
    if (config.cancel) return;
  }

  const res = await fetch(config.url, config);

  if (ctx.afterActor) {
    const ok = await ctx.afterActor(res);
    if (!ok) return;
  }

  if (ctx.swapper) {
    return ctx.swapper(res);
  }

  const markup = await res.text();
  return _swap(ctx, "innerHTML", markup);
}

function _swapper(ctx: ActorContext): Swapper & Retrigger {
  return {
    swap: (swapAction: string) => {
      ctx.swapper = async (res: Response) => {
        const markup = await res.text();
        return _swap(ctx, swapAction as SwapType, markup);
      };
      return {
        ..._actor(ctx)
      };
    },
    ..._retrigger(ctx)
  };
}

function _target(ctx: ActorContext): SwappableTarget {
  return {
    target: (selector: string) => {
      ctx.targetSelector = selector;
      return _swapper(ctx);
    },
    before: (fn: RequestConfigFn): SwappableTarget => {
      ctx.beforeActor = fn;
      return _target(ctx);
    },
    after: (fn: PostRequestFn): SwappableTarget => {
      ctx.afterActor = fn;
      return _target(ctx);
    },
    ..._swapper(ctx),
    ..._retrigger(ctx)
  };
}

function _retrigger(ctx: ActorContext): Retrigger {
  return {
    retrigger: () => {
      if (!ctx.actor) {
        throw new Error("No actor available");
      }
      ctx.actor(null);
    }
  };
}

function _actor(ctx: ActorContext): RetriggerableActor {
  const src = document.querySelector(ctx.sourceSelector) as HTMLElement;
  if (!src) {
    throw new Error("source element not found");
  }
  if (!ctx.triggerEvent) {
    switch (src.tagName) {
      case "BUTTON":
        ctx.triggerEvent = "click";
        break;
      case "FORM":
        ctx.triggerEvent = "submit";
        break;
      default:
        ctx.triggerEvent = "kilo:load";
        break;
    }
  }
  src.addEventListener(ctx.triggerEvent, ctx.trigger);
  return {
    get: (url: Path) => {
      ctx.actor = async (ev: Event | null) => {
        ev?.preventDefault();
        _doRequest(ctx, url, "GET");
      };
      return {
        ..._target(ctx),
        ..._retrigger(ctx)
      };
    },
    post: (url: Path) => {
      ctx.actor = async (ev: Event | null) => {
        ev?.preventDefault();
        _doRequest(ctx, url, "POST");
      };
      return {
        ..._target(ctx),
        ..._retrigger(ctx)
      };
    },
    do: (fn: EventHandler) => {
      ctx.actor = async (ev: Event | null) => {
        if (!ev) return;
        new Promise((resolve) => {
          fn(ev);
          resolve(null);
        });
      };
      return {
        ..._target(ctx),
        ..._retrigger(ctx)
      };
    },
    ..._retrigger(ctx)
  };
}

function _trigger(ctx: ActorContext): Trigger {
  return {
    on: (event: string) => {
      ctx.triggerEvent = event;
      return _actor(ctx);
    }
  };
}

function _sse(src: SSEEventSource): SSE {
  return {
    onError: (handler: (ev: Event) => void) => {
      src.errorHandler = handler;
      return _sse(src);
    },
    pause: (isPaused: boolean) => {
      src.isPaused = isPaused;
      if (!src.messageHandler) return _sse(src);
      if (!src.isPaused) {
        while (src.queue.length > 0) {
          const ev = src.queue.shift();
          if (!ev) continue;
          src.messageHandler(ev);
        }
        src.queue = [];
      }
      return _sse(src);
    },
    clear: () => {
      src.queue = [];
    },
    close: () => {
      src.source.close();
      actorRegistry.sseSources = actorRegistry.sseSources.filter(
        (s) => s !== src
      );
    }
  };
}

function _base(): Base {
  return {
    baseUrl: (url: string) => {
      baseUrl = url;
      return {
        ..._base()
      };
    },
    sse: (url: string, options: any): SSE => {
      const opts = {
        withCredentials: true,
        ...options
      };
      const src: SSEEventSource = {
        source: new EventSource(baseUrl + url, opts),
        isPaused: false,
        messageHandler: undefined,
        errorHandler: undefined,
        queue: []
      };
      src.messageHandler = defaultSSEEventHandler(src);
      src.source.onerror = (ev: Event) => {
        if (src.errorHandler) {
          src.errorHandler(ev);
        }
      };
      actorRegistry.sseSources.push(src);
      return _sse(src);
    }
  };
}

function _binder<T>(ctx: StateContext<T>) {
  return (field: keyof T, selector: string) => {
    const el = document.querySelector(selector) as HTMLElement;
    if (!el) {
      throw new Error("Element not found");
    }
    const handler = (ev: Event) => {
      const tgt = ev.target as HTMLElement;
      switch (tgt.tagName) {
        case "INPUT":
          const input = tgt as HTMLInputElement;
          switch (input.type) {
            case "checkbox":
              ctx.state[field as keyof UnwrapNestedRefs<T>] =
                input.checked as any;
              break;
            default:
              ctx.state[field as keyof UnwrapNestedRefs<T>] =
                input.value as any;
              break;
          }
          break;
        case "SELECT":
          ctx.state[field as keyof UnwrapNestedRefs<T>] = (
            tgt as HTMLSelectElement
          ).value as any;
          break;
        case "TEXTAREA":
          ctx.state[field as keyof UnwrapNestedRefs<T>] = (
            tgt as HTMLTextAreaElement
          ).value as any;
          break;
      }
    };

    const binding = {
      field: field as string,
      selector: selector,
      events: [] as string[],
      element: el,
      handler: handler
    };
    binding.events.push("change");
    ctx.bindings[field] = binding;

    switch (el.tagName) {
      case "INPUT":
        const input = el as HTMLInputElement;
        switch (input.type) {
          case "text":
            binding.events.push("keyup");
            break;
        }
        break;
      case "SELECT":
        binding.events.push("select");
        break;
      case "TEXTAREA":
        binding.events.push("keyup");
        break;
    }
    for (const event of binding.events) {
      el.addEventListener(event, handler);
    }
    return {
      ..._state(ctx)
    };
  };
}
function _state<T>(ctx: StateContext<T>): Model<T> & State<T> {
  return {
    model: ctx.state,
    watch: (
      field: keyof T,
      handler: (state: any, prev: any) => void | Promise<void>
    ) => {
      watch(
        () => ctx.state[field as keyof UnwrapNestedRefs<T>],
        async (state: any, prev: any) => {
          await handler(state, prev);
        }
      );
      return {
        ..._state(ctx)
      };
    },
    bind: _binder(ctx)
  };
}

export function actor(selector: string): Trigger & Actor {
  const src = document.querySelector(selector) as HTMLElement;
  if (!src) {
    throw new Error("source element not founf");
  }

  const ctx: ActorContext = {
    sourceSelector: selector,
    targetSelector: null,
    trigger: async (ev: Event) => {
      if (!ctx.actor) {
        throw new Error("No event handler specified");
      }
      ctx.actor(ev);
    },
    triggerEvent: null,
    actor: null,
    swapper: null,
    beforeActor: null,
    afterActor: null
  };

  actorRegistry.contexts.push(ctx);

  return {
    ..._trigger(ctx),
    ..._actor(ctx)
  };
}

export function state<T>(initialState: T & object): Model<T> & State<T> {
  const ctx: StateContext<T> = {
    state: reactive(initialState),
    bindings: {} as Record<keyof T, BindingEntry>
  };

  stateRegistry.contexts.push(ctx);

  return {
    ..._state(ctx)
  };
}

export function kilo<T>(): Base {
  return _base();
}
