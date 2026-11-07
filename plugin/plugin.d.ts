export interface Context {
  workspaceId: string
  requestId: string
  getValue(key: string): string
  setValue(key: string, value: string): void
}

export interface Field {
  name: string
  label?: string
  default?: string
  secret?: boolean
  options?: string[]
}

export interface Request {
  method: string
  url: string
  headers: Record<string, string | string[]>
  body: Uint8Array | string
}

export interface Response {
  status: number
  headers: Record<string, string[]>
  body: Uint8Array
}

export interface Template {
  name: string
  description?: string
  fields?: Field[]
  run(ctx: Context, args: Record<string, string>): string | Promise<string>
}

export interface Authentication {
  name: string
  label?: string
  fields?: Field[]
  apply(ctx: Context, values: Record<string, string>, request: Request): void | Promise<void>
}

export interface Action {
  name: string
  label?: string
  run(ctx: Context): void | Promise<void>
}

export interface Theme {
  name: string
  dark?: boolean
  colors: Record<string, string>
}

export interface Plugin {
  name: string
  version?: string
  description?: string
  templates?: Template[]
  authentication?: Authentication[]
  actions?: Action[]
  themes?: Theme[]
  import?(ctx: Context, content: string): Record<string, unknown>[] | undefined | Promise<Record<string, unknown>[] | undefined>
  filter?(ctx: Context, content: string, expression: string): string | Promise<string>
  beforeSend?(ctx: Context, request: Request): void | Promise<void>
  afterReceive?(ctx: Context, response: Response): void | Promise<void>
}
