declare type Box = {value: T, getValue<T>(defaultValue: T): number | T};
declare const Box: {new (value: T): Box<T>};
declare const box: Box<number>;
declare const a: number | string;
declare const b: number | 10;
