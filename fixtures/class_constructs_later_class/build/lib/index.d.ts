export declare type A = {v: number};
export declare const A: {new (v: number): A, mk(): B};
export declare type B = {n: number, toA(): A};
export declare const B: {new (n: number): B};
