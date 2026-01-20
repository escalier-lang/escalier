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
  throw "error";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp2 = "caught: " + msg;
  } else {
    throw __error;
  }
}
export const tryCatchWithThrow = temp2;
let temp3;
try {
  throw Error("fail");
} catch (__error) {
  if (true) {
    const Error = __error;
    temp3 = "caught error";
  } else {
    temp3 = "unknown";
  }
}
export const multipleCases = temp3;
let temp4;
try {
  throw "fail";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp4 = msg;
  } else {
    temp4 = "unknown";
  }
}
export const patternBinding = temp4;
let temp5;
try {
  throw "critical";
} catch (__error) {
  if (true) {
    const err = __error;
    if (err == "critical") {
      temp5 = "critical error";
    } else if (true) {
      temp5 = "other error";
    }
  }
}
export const withGuard = temp5;
let temp6;
try {
  temp6 = 42;
}
export const tryOnly = temp6;
export function safeDivide(temp7, temp8) {
  const a = temp7;
  const b = temp8;
  let temp9;
  try {
    temp9 = a / b;
  } catch (__error) {
    if (true) {
      temp9 = 0;
    }
  }
  return temp9;
}
let temp10;
try {
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp10 = msg;
  } else {
    temp10 = "unknown";
  }
}
export const objectPattern = temp10;
let temp11;
try {
  let temp12;
  try {
    temp12 = 5;
  } catch (__error) {
    if (true) {
      temp12 = 10;
    }
  }
  temp11 = temp12;
} catch (__error) {
  if (true) {
    temp11 = 0;
  }
}
export const nestedTryCatch = temp11;
let temp13;
try {
  const x = 5;
  temp13 = x + 10;
} catch (__error) {
  if (true) {
    const y = 0;
    temp13 = y;
  }
}
export const blockBody = temp13;
let temp14;
try {
  temp14 = 42;
} catch (__error) {
  if (true) {
    temp14 = "error";
  }
}
export const mixedReturn = temp14;
//# sourceMappingURL=./index.js.map
