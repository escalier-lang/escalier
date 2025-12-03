export const config = {apiUrl: "https://api.example.com", timeout: 5000};
export class Point {
  constructor(temp1, temp2) {
    const x = temp1;
    const y = temp2;
    this.x = x;
    this.y = y;
  }
}
export const point = new Point(10, 20);
export const immutablePoint = new Point(5, 15);
export const apiUrl = config.apiUrl;
export const pointX = point.x;
export const immutableX = immutablePoint.x;
export function updateMutableProperties() {
  point.y = 25;
}
export function tryUpdateImmutableProperties() {
  config.apiUrl = "https://foo.example.com";
  point.x = 0;
}
export class Counter {
  constructor(temp3) {
    const value = temp3;
    this.value = value;
  }
  increment() {
    this.value = this.value + 1;
  }
}
export const counter = new Counter(0);
//# sourceMappingURL=./index.js.map
