export declare function count(): Generator<1 | 2 | 3, void, never>;
export declare function countWithDone(): Generator<1 | 2, "done", never>;
export declare function mixed(): Generator<1 | "hello", void, never>;
