let temp1;
{
  const value = 15;
  let temp2;
  if (value > 10) {
    temp2 = "large";
  } else {
    temp2 = "small";
  }
  temp1 = temp2;
}
export const conditional = temp1;
let temp3;
{
  temp3 = undefined;
}
export const empty = temp3;
let temp4;
{
  const greeting = "Hello";
  const name = "World";
  temp4 = greeting + ", " + name + "!";
}
export const message = temp4;
let temp5;
{
  let temp6;
  {
    const inner = 10;
    temp6 = inner * 2;
  }
  const outer = temp6;
  temp5 = outer + 5;
}
export const nested = temp5;
let temp7;
{
  const x = 42;
  temp7 = console.log("Side effect executed");
}
export const sideEffect = temp7;
let temp8;
{
  const a = 5;
  const b = 10;
  temp8 = a + b;
}
export const sum = temp8;
//# sourceMappingURL=./index.js.map
