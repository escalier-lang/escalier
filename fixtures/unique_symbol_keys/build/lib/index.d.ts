declare const sym: unique symbol;
export declare type C = {[sym]: number, get value(): number};
export declare const C: {new (_field1: number): C};
export declare const c: C;
export declare const r: number;
export declare function store(c: C, v: number): void;
