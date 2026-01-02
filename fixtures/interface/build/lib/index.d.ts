export declare interface Person {
  name: string;
  age: number;
}
export declare interface User {
  id: number;
  username: string;
  email?: string;
}
export declare interface Calculator {
  add(a: number, b: number): number;
  subtract(a: number, b: number): number;
}
export declare interface Box<T> {
  value: T;
  isEmpty(): boolean;
}
export declare interface Point {
  readonly x: number;
  readonly y: number;
}
export declare interface Employee extends Person {
  employeeId: number;
  department: string;
}
export declare interface Manager extends Person, Employee {
  teamSize: number;
}
export declare interface Comparable<T extends number | string> {
  compareTo(other: T): number;
}
export declare interface Container<T> extends Box {
  size: number;
  items: T;
}
