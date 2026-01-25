declare type FooBar = {kind: "foo", id: number, foo: string} | {kind: "bar", id: number, bar: boolean};
declare const fb: FooBar;
declare const bar: undefined | boolean;
declare const foo: string | undefined;
declare const id: number | number;
declare const kind: "foo" | "bar";
