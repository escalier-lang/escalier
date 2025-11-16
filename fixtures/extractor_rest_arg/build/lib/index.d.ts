declare type C = {msg: string, value: number, flag: boolean};
declare const C: {new (msg: string, value: number, flag: boolean): C, [Symbol.customMatcher](subject: C): [string, number, boolean]};
declare const subject: C;
declare const x: string;
declare const y: [number, boolean];
