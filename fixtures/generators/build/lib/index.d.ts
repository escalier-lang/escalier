export declare function count(): Generator<1 | 2 | 3, void, never>;
export declare function countArray(): [...Array<1 | 2 | 3>];
export declare function countWithDone(): Generator<1 | 2, "done", never>;
export declare function fetchItems(): AsyncGenerator<1 | 2 | 3, void, never>;
export declare function inner(): Generator<1 | 2, void, never>;
export declare function mixed(): Generator<1 | "hello", void, never>;
export declare function outer(): Generator<1 | 2 | 3, void, never>;
export declare function outerArray(): [...Array<1 | 2 | 3>];
export declare function sumOuter(): number;
