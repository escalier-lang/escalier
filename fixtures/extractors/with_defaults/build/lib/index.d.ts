declare type C = {msg: string};
declare const C: {new (msg: string): C, [Symbol.customMatcher](subject: C): [string | undefined]};
declare const subject: C;
declare const msg: string | "world";
