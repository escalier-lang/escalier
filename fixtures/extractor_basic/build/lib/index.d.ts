declare type C = {msg: string};
declare const C: {new (msg: string): C, [Symbol.customMatcher](subject: C): [string]};
declare const subject: C;
declare const msg: string;
