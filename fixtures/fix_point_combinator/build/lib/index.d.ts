declare type Fix<A, B> = {f: (f: (arg: A) => B) => (arg: A) => B, recurse(arg: A): B};
declare const Fix: {new <A, B>(f: (f: (arg: A) => B) => (arg: A) => B): Fix<A, B>};
declare const fact: (cont: (n: number) => number) => (n: number) => number;
declare const fix: <A, B>(f: (f: (arg: A) => B) => (arg: A) => B) => (arg: A) => B;
declare const result: number;
