declare type FooBar = {kind: "foo", id: number, foo: string} | {kind: "bar", id: number, bar: boolean};
declare const fb: FooBar;
declare const kind: "foo" | "bar";
declare const id: number | number;
declare const foo: string | undefined;
declare const bar: undefined | boolean;
