declare type Bag<T> = {items: Array<T>, contains<B>(x: B): boolean};
declare const Bag: {new <T>(items: Array<T>): Bag<T>};
declare type Widen<B> = B;
declare function first<B>(xs: Array<B>): B;
