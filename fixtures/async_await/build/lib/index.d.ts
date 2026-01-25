export declare async function conditionalFetch(useCache: boolean, url: string): Promise<"cached data" | Response, never>;
export declare async function fetchData(url: string): Promise<string, never>;
export declare async function fetchMultiple(url1: string, url2: string): Promise<string, never>;
export declare async function fetchWithAwait(url: string): Promise<Response, never>;
export declare async function fetchWithError(shouldError: boolean): Promise<"success", "Something went wrong">;
