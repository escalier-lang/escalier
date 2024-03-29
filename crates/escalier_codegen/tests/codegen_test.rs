use escalier_codegen::d_ts::codegen_d_ts;
use escalier_codegen::js::codegen_js;
use escalier_hm::checker::Checker;
use escalier_hm::context::Context;
use escalier_hm::type_error::TypeError;
use escalier_parser::parse;

fn compile(input: &str) -> (String, String) {
    let program = parse(input).unwrap();
    codegen_js(input, &program)
}

#[test]
fn js_print_multiple_decls() {
    let (js, _) = compile("let foo = \"hello\"\nlet bar = \"world\"");

    insta::assert_snapshot!(js, @r###"
    export const foo = "hello";
    export const bar = "world";
    "###);
}

#[test]
fn unary_minus() {
    let src = r#"
    let negate = fn (x) => -x
    "#;

    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const negate = (x)=>-x;
");
}

#[test]
fn fn_with_block_without_return() {
    let src = r#"
    let foo = fn (x, y) {
        let z = x + y
        z
    }
    "#;

    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const foo = (x, y)=>{
        const z = x + y;
        z;
    };
    "###);
}

#[test]
fn fn_with_block_with_return() {
    let src = r#"
    let foo = fn (x, y) {
        let z = x + y
        return z
    }
    "#;

    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const foo = (x, y)=>{
        const z = x + y;
        return z;
    };
    "###);
}

#[test]
fn template_literals() {
    let src = r#"
    let a = `hello, world`
    let p = {x: 5, y: 10}
    console.log(`p = (${p.x}, ${p.y})`)
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const a = `hello, world`;
    export const p = {
        x: 5,
        y: 10
    };
    console.log(`p = (${p.x}, ${p.y})`);
    "###);
}

#[test]
fn tagged_template_literals() {
    let src = r#"
    let id = "12345"
    let query = sql`SELECT * FROM users WHERE id = "${id}"`
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const id = "12345";
    export const query = sql`SELECT * FROM users WHERE id = "${id}"`;
    "###);
}

#[test]
fn pattern_matching() {
    let src = r#"
    let result = match (count + 1) {
        0 => "none",
        1 => "one",
        2 => "a couple",
        n if (n < 5) => {
            console.log(`n = ${n}`)
            "a few"
        },
        _ => {
            console.log("fallthrough")
            "many"
        }
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    const $temp_1 = count + 1;
    if ($temp_1 === 0) {
        $temp_0 = "none";
    } else if ($temp_1 === 1) {
        $temp_0 = "one";
    } else if ($temp_1 === 2) {
        $temp_0 = "a couple";
    } else if (n < 5) {
        const n = $temp_1;
        console.log(`n = ${n}`);
        $temp_0 = "a few";
    } else {
        const $temp_2 = $temp_1;
        console.log("fallthrough");
        $temp_0 = "many";
    }
    export const result = $temp_0;
    "###);
}

#[test]
fn pattern_matching_with_disjoint_union() -> Result<(), TypeError> {
    let src = r#"
    type Event = {type: "mousedown", x: number, y: number} | {type: "keydown", key: string}
    declare let event: Event
    let result = match (event) {
        {type: "mousedown", x, y} => `mousedown: (${x}, ${y})`,
        {type: "keydown", key} if (key != "Escape") => key
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    ;
    ;
    let $temp_0;
    const $temp_1 = event;
    if ($temp_1.type === "mousedown") {
        const { x, y } = $temp_1;
        $temp_0 = `mousedown: (${x}, ${y})`;
    } else if ($temp_1.type === "keydown" && key !== "Escape") {
        const { key } = $temp_1;
        $temp_0 = key;
    }
    export const result = $temp_0;
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"
    declare type Event = {
        type: "mousedown";
        x: number;
        y: number;
    } | {
        type: "keydown";
        key: string;
    };
    export declare const event: Event;
    export declare const result: string | string;
    "###);

    Ok(())
}

#[test]
// TODO: Have a better error message when there's multiple catch-alls
#[should_panic = "Catchall must appear last in match"]
fn pattern_matching_multiple_catchall_panics() {
    let src = r#"
    let result = match (value) {
        n => "foo",
        _ => "bar"
    }
    "#;

    compile(src);
}

#[test]
#[should_panic = "No arms in match"]
fn pattern_matching_no_arms_panics() {
    let src = r#"
    let result = match (value) {
    }
    "#;

    compile(src);
}

#[test]
fn simple_if_else() {
    let src = r#"
    let result = if (cond) {
        console.log("true")
        5
    } else {
        console.log("false")
        10
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    if (cond) {
        console.log("true");
        $temp_0 = 5;
    } else {
        console.log("false");
        $temp_0 = 10;
    }
    export const result = $temp_0;
    "###);
}

#[test]
fn simple_if_else_inside_fn() {
    let src = r#"
    let foo = fn () {
        let result = if (cond) {
            console.log("true")
            5
        } else {
            console.log("false")
            10
        }
        return result
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const foo = ()=>{
        let $temp_0;
        if (cond) {
            console.log("true");
            $temp_0 = 5;
        } else {
            console.log("false");
            $temp_0 = 10;
        }
        const result = $temp_0;
        return result;
    };
    "###);
}

#[test]
fn simple_if_else_inside_fn_as_expr() {
    let src = r#"
    let foo = fn () => if (cond) {
        console.log("true")
        5
    } else {
        console.log("false")
        10
    }
    "#;
    let (js, _) = compile(src);

    // TODO: all of this code should be inside the body of
    // the function, not outside it
    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    if (cond) {
        console.log("true");
        $temp_0 = 5;
    } else {
        console.log("false");
        $temp_0 = 10;
    }
    export const foo = ()=>$temp_0;
    "###);
}

#[test]
fn nested_if_else() {
    let src = r#"
    let result = if (c1) {
        if (c2) {
            5
        } else {
            10
        }
    } else {
        if (c3) {
            "hello"
        } else {
            "world"
        }
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    if (c1) {
        let $temp_1;
        if (c2) {
            $temp_1 = 5;
        } else {
            $temp_1 = 10;
        }
        $temp_0 = $temp_1;
    } else {
        let $temp_2;
        if (c3) {
            $temp_2 = "hello";
        } else {
            $temp_2 = "world";
        }
        $temp_0 = $temp_2;
    }
    export const result = $temp_0;
    "###);
}

#[test]
fn multiple_lets_inside_a_function() {
    let src = r#"
    let do_math = fn () {
        let x = 5
        let y = 10
        let result = x + y
        return result
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const do_math = ()=>{
        const x = 5;
        const y = 10;
        const result = x + y;
        return result;
    };
    "###);
}

// TODO: do we want to support `if-let`?
#[test]
#[ignore]
fn codegen_if_let_with_rename() {
    // TODO: don't allow irrefutable patterns to be used with if-let
    let src = r#"
    let result = if (let {x: a, y: b} = {x: 5, y: 10}) {
        a + b
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    const $temp_1 = {
        x: 5,
        y: 10
    };
    {
        const { x: a, y: b } = $temp_1;
        $temp_0 = a + b;
    }export const result = $temp_0;
    "###);
}

// TODO: do we want to support `if-let`?
#[test]
#[ignore]
fn codegen_if_let_refutable_pattern_nested_obj() {
    let src = r#"
    let action = {type: "moveto", point: {x: 5, y: 10}}
    if (let {type: "moveto", point: {x, y}} = action) {
        x + y
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const action = {
        type: "moveto",
        point: {
            x: 5,
            y: 10
        }
    };
    let $temp_0;
    const $temp_1 = action;
    if ($temp_1.type === "moveto") {
        const { point: { x, y } } = $temp_1;
        $temp_0 = x + y;
    }
    $temp_0;
    "###);
}

// TODO: do we want to support `if-let`?
#[test]
#[ignore]
fn codegen_if_let_with_else() {
    let src = r#"
    declare let a: string | number
    let result = if (let x is number = a) {
        x + 5
    } else if (let y is string = a) {
        y
    } else {
        true
    }
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    ;
    let $temp_0;
    const $temp_1 = a;
    if (typeof $temp_1 === "number") {
        const x = $temp_1;
        $temp_0 = x + 5;
    } else {
        let $temp_2;
        const $temp_3 = a;
        if (typeof $temp_3 === "string") {
            const y = $temp_3;
            $temp_2 = y;
        } else {
            $temp_2 = true;
        }
        $temp_0 = $temp_2;
    }
    export const result = $temp_0;
    "###);
}

#[test]
fn codegen_block_with_multiple_non_let_lines() {
    let src = r#"let result = do {
        let x = 5
        x + 0
        x
    }"#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    {
        const x = 5;
        x + 0;
        $temp_0 = x;
    }export const result = $temp_0;
    "###);
}

#[test]
fn destructuring_function_object_params() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn ({x, y: b}) => x + b
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const foo = ({ x, y: b })=>x + b;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx).unwrap();
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"
    export declare const foo: ({ x, y: b }: {
        x: number;
        y: number;
    }) => number;
    "###);

    Ok(())
}

#[test]
fn destructuring_function_array_params() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn ([a, b]) => a + b
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const foo = ([a, b])=>a + b;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const foo: ([a, b]: readonly [number, number]) => number;
");

    Ok(())
}

#[test]
fn function_with_rest_param() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn (x: number, ...y: number[]) => x
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const foo = (x, ...y)=>x;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const foo: (x: number, ...y: readonly number[]) => number;
");

    Ok(())
}

#[test]
fn function_with_optional_param() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn (x: number, y?: number) => x
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const foo = (x, y)=>x;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const foo: (x: number, y?: number) => number;
");

    Ok(())
}

// TODO: `x` should have type `number | undefined` not `number`
// TODO: `y` should have type `readonly number[]` not `number[]`
#[test]
#[ignore]
fn function_with_optional_param_and_rest_param() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn (x?: number, ...y: number[]) => x
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const foo = (x, ...y)=>x;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const foo: (x?: number, ...y: Array<number>) => number;
");

    Ok(())
}

#[test]
fn generic_function() -> Result<(), TypeError> {
    let src = r#"
    let fst = fn <T>(a: T, b: T) => a
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const fst = (a, b)=>a;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // TODO: the return type should be `T` not `unknown`
    insta::assert_snapshot!(result, @"export declare const fst: <T>(a: T, b: T) => T;
");

    Ok(())
}

#[test]
fn constrained_generic_function() -> Result<(), TypeError> {
    let src = r#"
    let fst = fn <T: number | string>(a: T, b: T) => a
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @"export const fst = (a, b)=>a;
");

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // TODO: The type bound on `T` should be `number | string`, not `number | number`
    // TODO: The return type should be `T`, not `number | string`
    insta::assert_snapshot!(result, @"export declare const fst: <T extends number | string>(a: T, b: T) => T;
");

    Ok(())
}

#[test]
fn variable_declaration_with_destructuring() -> Result<(), TypeError> {
    let src = r#"
    let [x, y] = [5, 10]
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const [x, y] = [
        5,
        10
    ];
    "###);

    // TODO: Support destructuring in top-level decls
    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"
    export declare const x: 5;
    export declare const y: 10;
    "###);

    Ok(())
}

#[test]
fn computed_property() {
    let src = r#"
    let p = {x: 5, y: 10}
    let x = p["x"]
    let q = [5, 10]
    let y = q[1]
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const p = {
        x: 5,
        y: 10
    };
    export const x = p["x"];
    export const q = [
        5,
        10
    ];
    export const y = q[1];
    "###);
}

// TODO: handle spreading args
#[test]
#[ignore]
fn spread_args() {
    let src = r#"
    let add = fn (a, b) => a + b
    let sum = add(...[5, 10])
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const add = (a, b)=>a + b;
    export const sum = add(...[
        5,
        10
    ]);
    "###);
}

#[test]
fn mutable_array() -> Result<(), TypeError> {
    let src = r#"
    let mut arr: number[] = [1, 2, 3]
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const arr = [
        1,
        2,
        3
    ];
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const arr: readonly number[];
");

    Ok(())
}

#[test]
fn mutable_obj() -> Result<(), TypeError> {
    let src = r#"
    type Point = {x: number, y: number}
    "#;

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // This should be:
    insta::assert_snapshot!(result, @r###"
    declare type Point = {
        x: number;
        y: number;
    };
    declare type ReadonlyPoint = {
        readonly x: number;
        readonly y: number;
    };
    "###);

    Ok(())
}

// TODO: finish porting codgen_d_ts()
#[test]
#[ignore]
fn mutable_indexer() -> Result<(), TypeError> {
    let src = r#"
    type Dict = {[P]: string for P in string}
    "#;

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"
    declare type Dict = {
        [key: string]: string;
    };
    declare type ReadonlyDict = {
        readonly [key: string]: string;
    };
    "###);

    Ok(())
}

// TODO: finish porting class handling code
#[test]
#[ignore]
fn class_with_methods() {
    let src = r#"
    class Foo {
        x: number
        constructor: fn (self, x) {
            self.x = x
        }
        foo: fn (self, y) {
            return self.x + y
        }
        bar: fn (self, y) {
            self.x + y
        }
    }
    "#;

    let (js, _srcmap) = compile(src);

    insta::assert_snapshot!(js, @r###"
    class Foo {
        constructor(x){
            self.x = x;
        }
        foo(y) {
            return self.x + y;
        }
        bar(y) {
            self.x + y;
        }
    }
    "###);
}

#[test]
fn for_loop() -> Result<(), TypeError> {
    let src = r#"
    let mut sum: number = 0
    for (num in [1, 2, 3]) {
        sum = sum + num
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    export const sum = 0;
    for (const num of [
        1,
        2,
        3
    ]){
        sum = sum + num;
    }
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"export declare const sum: number;
    "###);

    Ok(())
}

#[test]
fn for_loop_inside_fn() -> Result<(), TypeError> {
    let src = r#"
    let sum = fn (arr: number[]) {
        let mut result: number = 0
        for (num in arr) {
            result = result + num
        }
        return result
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    export const sum = (arr)=>{
        const result = 0;
        for (const num of arr){
            result = result + num;
        }
        return result;
    };
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const sum: (arr: readonly number[]) => number;
");

    Ok(())
}

#[test]
fn type_decl_inside_block() -> Result<(), TypeError> {
    let src = r#"
    let result = do {
        type Point = {x: number, y: number}
        let p: Point = {x: 5, y: 10}
        p.x + p.y
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    {
        const p = {
            x: 5,
            y: 10
        };
        $temp_0 = p.x + p.y;
    }export const result = $temp_0;
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const result: number;
");

    Ok(())
}

#[test]
fn type_decl_inside_block_with_escape() -> Result<(), TypeError> {
    let src = r#"
    let result = do {
        type Point = {x: number, y: number}
        let p: Point = {x: 5, y: 10}
        p
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    {
        const p = {
            x: 5,
            y: 10
        };
        $temp_0 = p;
    }export const result = $temp_0;
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // TODO: How do we ensure that types defined within a block can't escape?
    insta::assert_snapshot!(result, @"export declare const result: Point;
");

    Ok(())
}

// TODO: handle class decls
#[test]
#[ignore]
fn class_inside_function() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn () {
        class Point {
            x: number
            y: number
            constructor: fn (self, x, y) {
                self.x = x
                self.y = y
            }
        }
        const p = new Point(5, 10)
        return p
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    export const foo = ()=>{
        class Point {
            constructor(x, y){
                self.x = x;
                self.y = y;
            }
        }
        const p = new Point(5, 10);
        return p;
    };
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @"export declare const foo: () => Point;
");

    Ok(())
}

#[test]
#[should_panic = "return statements aren't allowed at the top level"]
fn top_level_return() {
    let src = r#"
    return 5
    "#;
    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    export const add = (a, b)=>a + b;
    export const sum = add(...[
        5,
        10
    ]);
    "###);
}

#[test]
fn multiple_returns_stress_test() -> Result<(), TypeError> {
    let src = r#"
    let foo = fn (cond: boolean) {
        let bar = fn () {
            if (cond) {
                return 5
            }
        }
        if (cond) {
            return bar()
        }
        return 10
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    export const foo = (cond)=>{
        const bar = ()=>{
            let $temp_0;
            if (cond) {
                return 5;
            }
            $temp_0;
        };
        let $temp_1;
        if (cond) {
            return bar();
        }
        $temp_1;
        return 10;
    };
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // TODO: the return value should be `5 | 10 | undefined`
    insta::assert_snapshot!(result, @"export declare const foo: (cond: boolean) => undefined | 10;
");

    Ok(())
}

#[test]
fn partial_type() -> Result<(), TypeError> {
    let src = r#"
    type Partial<T> = { [P]+?: T[P] for P in keyof T }
    type Obj = {a?: string, b: number}
    type PartialObj = Partial<Obj>
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    ;
    ;
    ;
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // TODO: How do we ensure that types defined within a block can't escape?
    insta::assert_snapshot!(result, @r###"
    declare type Obj = {
        a?: string;
        b: number;
    };
    declare type ReadonlyObj = {
        readonly a?: string;
        readonly b: number;
    };
    declare type Partial<T> = {
        [P in keyof T]: T[P];
    };
    declare type PartialObj = Partial<ReadonlyObj>;
    "###);

    Ok(())
}

#[test]
fn mapped_type_with_additional_props() -> Result<(), TypeError> {
    let src = r#"
    type Direction = "up" | "down" | "left" | "right"
    type Style = {
        background: string,
        color: string,
        [P]: boolean for P in Direction
    }
    "#;

    let (js, _) = compile(src);
    insta::assert_snapshot!(js, @r###"
    ;
    ;
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    // TODO: How do we ensure that types defined within a block can't escape?
    insta::assert_snapshot!(result, @r###"
    declare type Direction = "up" | "down" | "left" | "right";
    declare type Style = {
        background: string;
        color: string;
    } & {
        [P in Direction]: boolean;
    };
    declare type ReadonlyStyle = {
        readonly background: string;
        readonly color: string;
    } & {
        [P in Direction]: boolean;
    };
    "###);

    Ok(())
}

#[test]
fn compile_fib() -> Result<(), TypeError> {
    let src = r#"
    // only self-recursive functions are supported, but support for
    // mutual recursion will be added in the future
    let fib = fn (n) => if (n == 0) {
        0
    } else if (n == 1) {
        1
    } else {
        fib(n - 1) + fib(n - 2)
    }
    "#;

    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    let $temp_0;
    if (n === 0) {
        $temp_0 = 0;
    } else if (n === 1) {
        $temp_0 = 1;
    } else {
        $temp_0 = fib(n - 1) + fib(n - 2);
    }
    export const fib = (n)=>$temp_0;
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"
    export declare const fib: (n: number) => 0 | 1 | number;
    "###);

    Ok(())
}

// TODO: infer JSX
#[test]
#[ignore]
fn compile_jsx() -> Result<(), TypeError> {
    let src = r#"
    let button = <Button count={5} foo="bar" />
    "#;

    let (js, _) = compile(src);

    insta::assert_snapshot!(js, @r###"
    import { jsx as _jsx } from "react/jsx-runtime";
    export const button = _jsx(Button, {
        count: 5,
        foo: "bar"
    });
    "###);

    let mut program = parse(src).unwrap();
    let mut checker = Checker::default();
    let mut ctx = Context::default();
    checker.infer_script(&mut program, &mut ctx)?;
    let result = codegen_d_ts(&program, &ctx, &checker)?;

    insta::assert_snapshot!(result, @r###"TODO"###);

    Ok(())
}
