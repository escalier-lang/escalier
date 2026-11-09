import { ValueType } from "@escalier/runtime";
export declare function first<T extends ValueType>(xs: Array<T>): T | undefined;
export declare function steal<T extends ValueType>(p: T): T;
export declare const v: ValueType;
