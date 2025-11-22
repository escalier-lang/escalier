declare type Pick<T, K extends keyof T> = {[P in K]: T[P]};
declare type Obj = {a: number, b: string, c: boolean};
declare const obj1: Pick<Obj, "a" | "c">;
declare const a: number;
declare const b: never;
declare const c: boolean;
