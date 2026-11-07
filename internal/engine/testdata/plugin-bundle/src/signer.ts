export enum Scheme {
  Basic = "Basic",
  Bearer = "Bearer",
}

export abstract class Credential {
  static #count = 0;
  readonly #secret: string;
  protected constructor(secret: string) {
    this.#secret = secret;
    Credential.#count++;
  }
  static get count(): number {
    return Credential.#count;
  }
  protected get secret(): string {
    return this.#secret;
  }
  abstract header(): string;
}

export class BasicCredential extends Credential {
  constructor(private readonly user: string, password: string) {
    super(password);
  }
  header(): string {
    return `${Scheme.Basic} ${btoa(`${this.user}:${this.secret}`)}`;
  }
}

export class BearerCredential extends Credential {
  constructor(token: string) {
    super(token);
  }
  header(): string {
    return `${Scheme.Bearer} ${this.secret}`;
  }
}

export function credential(values: Record<string, string>): Credential {
  return values.token ? new BearerCredential(values.token) : new BasicCredential(values.user ?? "", values.password ?? "");
}
