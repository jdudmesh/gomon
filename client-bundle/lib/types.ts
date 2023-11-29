import { type UnwrapNestedRefs } from "@vue/reactivity";

export type RequestConfig = {
  url: string
  cancel: boolean
  contentType: string
} & RequestInit

export type RequestConfigFn = (config: RequestConfig) => Promise<void> | void
export type PostRequestFn = (res: Response) => Promise<boolean> | boolean

export type ActorContext = {
  selector: string
  sourceElement: HTMLElement
  targetElement: HTMLElement | null
  triggerEvent: string | null
  trigger: TriggerHandlerFn
  actor: ActorHandlerFn | null
  swapper: ResponseHandlerFn | null
  beforeActor: RequestConfigFn | null
  afterActor: PostRequestFn | null
};

export type BindingEntry = {
  field: string
  selector: string
  events: string[]
  element: HTMLElement
  handler: EventHandler
}

export type StateContext<T> = {
  state: UnwrapNestedRefs<T>
  bindings: Record<keyof T, BindingEntry>
}

export interface Base {
  baseUrl: (url: string) => Base
  sse: (url: string, options: any) => SSE
}

export interface SSE {
  pause: (isPaused: boolean) => SSE
  clear: () => void
  close: () => void
}

export interface Swapper {
  swap: (swapAction: string) => RetriggerableActor
}

export type SwappableTarget = Target & Swapper & Retrigger

export interface Target {
  target: (selector: Selector) => Swapper & Retrigger
  before: (fn: RequestConfigFn) => SwappableTarget
  after: (fn: PostRequestFn) => SwappableTarget
}

export interface Actor {
  get: (url: Path) => SwappableTarget
  post: (url: Path) => SwappableTarget
}

export interface Retrigger {
  retrigger: () => void
}

export type RetriggerableActor = Actor & Retrigger

export interface Trigger {
  on: (event: string) => RetriggerableActor
}

export interface State<T> {
  watch: (field: keyof T, handler: (state: any, prev: any) => void|Promise<void>) => Model<T> & State<T>
  bind: (field: keyof T, selector: string) => Model<T> & State<T>
}

export interface Model<T> {
  model: UnwrapNestedRefs<T>
}

export type SSEEventSource = {
  source: EventSource;
  handler: EventHandler|undefined;
  isPaused: boolean;
  queue: MessageEvent[];
};

export type TriggerHandlerFn = (ev: Event) => Promise<void>;
export type ActorHandlerFn = (ev: Event | null) => Promise<void>;
export type ResponseHandlerFn = (res: Response) => Promise<void>;
export type SelectorFn = (ctx: ActorContext) => HTMLElement;
export type Selector = string | SelectorFn;
export type EventHandler = (ev: Event) => void;

export type PathFn = (ctx: ActorContext) => string;
export type Path = string | PathFn;

export type SwapType =
  | "innerHTML"
  | "outerHTML"
  | "beforebegin"
  | "afterbegin"
  | "beforeend"
  | "afterend"
  | "delete"
  | "none";

export type ActorRegistry = {
  state: DocumentReadyState;
  contexts: ActorContext[];
  sseSources: SSEEventSource[];
};

export type StateRegistry = {
  contexts: StateContext<any>[];
};

export interface KiloDef {
  "x-kilo-target": string;
  "x-kilo-swap": string;
  "x-kilo-markup": string;
}
