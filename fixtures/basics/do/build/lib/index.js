let temp1;
{
  export const a = 5;
  export const b = 10;
  temp1 = a + b;
}
export const sum = temp1;
let temp2;
{
  export const greeting = "Hello";
  export const name = "World";
  temp2 = greeting + ", " + name + "!";
}
export const message = temp2;
let temp3;
{
  export const x = 42;
  temp3 = console.log("Side effect executed");
}
export const sideEffect = temp3;
let temp4;
{
  let temp5;
  {
    export const inner = 10;
    temp5 = inner * 2;
  }
  export const outer = temp5;
  temp4 = outer + 5;
}
export const nested = temp4;
let temp6;
{
  export const value = 15;
  let temp7;
  if (value > 10) {
    temp7 = "large";
  } else {
    temp7 = "small";
  }
  temp6 = temp7;
}
export const conditional = temp6;
let temp8;
{
  temp8 = undefined;
}
export const empty = temp8;
//# sourceMappingURL=./index.js.map
