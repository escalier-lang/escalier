declare function split(s: string, delimiter: string): Array<string>;
declare type Person = {firstName: string, lastName: string, get fullName(): string, set fullName(value: string)};
declare const Person: {new (firstName: string, lastName: string): Person};
declare const person: Person;
declare function main(): void;
declare const name: string;
