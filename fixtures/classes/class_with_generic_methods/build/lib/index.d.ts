declare type Foo = {value: number, getValue<T>(defaultValue: T): number | T, addStrs1<A extends string, B extends string>(a: A, b: B): number, addNums2<A extends number, B extends number>(a: A, b: B): number};
declare const Foo: {new (): Foo, addNums1(a: number, b: number): number, addStrs3<A extends string, B extends string>(a: A, b: B): number, addNums4<A extends number, B extends number>(a: A, b: B): number};
declare const foo: Foo;
declare const a: number | string;
declare const b: number | 10;
declare const c: number;
declare const d: number;
