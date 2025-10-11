declare type C = {msg: string | undefined, value: number};
declare const C: {new (msg: string | undefined, value: number): C, [Symbol.customMatcher](subject: C): [string | undefined, number]};
declare const subject: C;
declare const x: string | undefined;
declare const y: number;
