declare const container: {value: number, getValue<T>(defaultValue: T): number | T, addNums1(a: number, b: number): number, addStrs1<A extends string, B extends string>(a: A, b: B): number, addNums2<A extends number, B extends number>(a: A, b: B): number};
declare const a: number | string;
declare const b: number | 10;
declare const c: number;
