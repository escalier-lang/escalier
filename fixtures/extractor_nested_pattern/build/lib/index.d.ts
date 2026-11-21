declare namespace Boxed {
  export type Of = {};
  export const Of: {new (value: {x: number}): Boxed, [Symbol.customMatcher](subject: Of): [{x: number}]};
}
declare type Boxed = Boxed.Of;
declare namespace Pair {
  export type Of = {};
  export const Of: {new (items: [number, number]): Pair, [Symbol.customMatcher](subject: Of): [[number, number]]};
}
declare type Pair = Pair.Of;
declare const first: number;
export declare function firstOf(arg0: Pair): number;
declare const x: number;
export declare const fromObject: number;
export declare const fromTuple: number;
