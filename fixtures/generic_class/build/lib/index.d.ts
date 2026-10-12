declare type Box<T> = {value: T, getValue<U>(defaultValue: U): T | U};
declare const Box: {new <T>(value: T): Box<T>};
declare const box: Box<number>;
declare const a: number | string;
declare const b: number;
