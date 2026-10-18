declare type Digits = string;
declare type Id = string | Digits;
export declare function classify(s: string): "digits" | "word" | "other";
export declare function firstDigits(p: [string, number]): number;
export declare function kind(v: string | number | boolean): "id" | "scalar" | "other";
