declare type Animal = {name: string, speak(): string, describe(): string};
declare const Animal: {new (name: string): Animal};
declare type Dog = {breed: string, speak(): string};
declare const Dog: {new (name: string, breed: string): Dog};
declare const d: Dog;
declare const described: string;
declare const heard: string;
declare const named: string;
