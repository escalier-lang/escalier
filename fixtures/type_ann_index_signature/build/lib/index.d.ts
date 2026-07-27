declare type Config = {readonly [K in string]?: boolean} & {name: string};
declare type Obj = {a: number, b: string};
declare type Copy = {[K in keyof Obj]: Obj[K]};
declare type Dict = {[K in string]?: number};
declare type LongDict = {[K in string]?: number};
declare const copy: Copy;
declare const a: number;
declare const b: string;
