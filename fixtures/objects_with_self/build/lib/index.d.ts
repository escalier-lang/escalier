declare const value: number;
interface __obj1_self__{value: number, increment(amount: number): this}
export declare const obj1: __obj1_self__;
interface __inc_self__(amount: number) => {value: number, increment(amount: number): this}
declare const inc: __inc_self__;
export declare const obj2: {_value: number, get value(): number, set value(value: number)};
