let temp1;
try {
  temp1 = "success";
} catch (__error) {
  if (true) {
    temp1 = "error";
  }
}
export const basicTryCatch = temp1;
let temp2;
try {
  const x = 5;
  temp2 = x + 10;
} catch (__error) {
  if (true) {
    const y = 0;
    temp2 = y;
  }
}
export const blockBody = temp2;
let temp3;
try {
  temp3 = 42;
} catch (__error) {
  if (true) {
    temp3 = "error";
  }
}
export const mixedReturn = temp3;
let temp4;
try {
  throw Error("fail");
} catch (__error) {
  if (true) {
    const Error = __error;
    temp4 = "caught error";
  } else {
    temp4 = "unknown";
  }
}
export const multipleCases = temp4;
let temp5;
try {
  let temp6;
  try {
    temp6 = 5;
  } catch (__error) {
    if (true) {
      temp6 = 10;
    }
  }
  temp5 = temp6;
} catch (__error) {
  if (true) {
    temp5 = 0;
  }
}
export const nestedTryCatch = temp5;
let temp7;
try {
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp7 = msg;
  } else {
    temp7 = "unknown";
  }
}
export const objectPattern = temp7;
let temp8;
try {
  throw "fail";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp8 = msg;
  } else {
    temp8 = "unknown";
  }
}
export const patternBinding = temp8;
export function safeDivide(temp9, temp10) {
  const a = temp9;
  const b = temp10;
  let temp11;
  try {
    temp11 = a / b;
  } catch (__error) {
    if (true) {
      temp11 = 0;
    }
  }
  return temp11;
}
let temp12;
try {
  throw "error";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp12 = "caught: " + msg;
  } else {
    throw __error;
  }
}
export const tryCatchWithThrow = temp12;
let temp13;
try {
  temp13 = 42;
}
export const tryOnly = temp13;
let temp14;
try {
  throw "critical";
} catch (__error) {
  if (true) {
    const err = __error;
    if (err == "critical") {
      temp14 = "critical error";
    } else if (true) {
      temp14 = "other error";
    }
  }
}
export const withGuard = temp14;
//# sourceMappingURL=./index.js.map
