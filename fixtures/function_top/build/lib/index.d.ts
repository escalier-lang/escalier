declare type Box<T> = {value: T};
declare const Box: {new <T>(value: T): Box<T>};
export declare function describe<T extends Function>(f: T): string;
export declare const fs: Array<Function>;
export declare function kind(u: number | ((x: number) => number)): string;
