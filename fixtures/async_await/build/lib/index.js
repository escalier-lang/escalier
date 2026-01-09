export async function fetchData(temp1) {
  const url = temp1;
  return "data from " + url;
}
export async function fetchWithAwait(temp2) {
  const url = temp2;
  const data = await fetch(url);
  return data;
}
export async function fetchWithError(temp3) {
  const shouldError = temp3;
  let temp4;
  if (shouldError) {
    let temp5;
    throw "Something went wrong";
  }
  temp4;
  return "success";
}
export async function fetchMultiple(temp6, temp7) {
  const url1 = temp6;
  const url2 = temp7;
  const data1 = await fetch(url1);
  const text1 = await data1.text();
  const data2 = await fetch(url2);
  const text2 = await data2.text();
  return text1 + " and " + text2;
}
export async function conditionalFetch(temp8, temp9) {
  const useCache = temp8;
  const url = temp9;
  let temp10;
  if (useCache) {
    return "cached data";
  } else {
    const data = await fetch(url);
    return data;
  }
  temp10;
}
//# sourceMappingURL=./index.js.map
