declare type A = {kind: "a", a: number};
declare type B = {kind: "b", b: string};
declare type U = A | B;
export declare function plain(u: U): number;
export declare const fromA: number;
export declare const fromB: number;
