package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferJSX covers JSX elements and fragments typed against `@types/react`. Valid cases
// expect no diagnostics. The expected messages are a starting point, and the implementation
// may settle on its own wording.
func TestInferJSX(t *testing.T) {
	t.Skip("JSX does not type-check in internal/solver, #2100")
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "an intrinsic element with props and children",
			src:  `val elem = <div className="foo" id="bar"><span>Hello</span></div>`,
		},
		{
			name: "a fragment",
			src:  `val elem = <><div /><span /></>`,
		},
		{
			name: "an event handler prop",
			src:  `val elem = <button onClick={fn() { }} />`,
		},
		{
			name: "a key prop",
			src:  `val elem = <div key="item-1" />`,
		},
		{
			name: "a component with props",
			src: `fn Button(props: {label: string}) {
	return <button>{props.label}</button>
}
val elem = <Button label="Click me" />`,
		},
		{
			name: "a member expression component",
			src: `val Icons = {
	Star: fn(props: {size: number}) {
		return <span>★</span>
	},
}
val elem = <Icons.Star size={24} />`,
		},
		{
			name: "a component with a string child",
			src: `fn Container(props: {children: string}) {
	return <div>{props.children}</div>
}
val elem = <Container>Hello</Container>`,
		},
		{
			name:     "an intrinsic prop of the wrong type",
			src:      `val elem = <div className={123} />`,
			wantErrs: []string{"123 cannot be assigned to string | undefined"},
		},
		{
			name: "a component missing a required prop",
			src: `fn MyComponent(props: {title: string, count: number}) {
	return <div>{props.title}</div>
}
val elem = <MyComponent count={5} />`,
			wantErrs: []string{"Missing required prop 'title' for {title: string, count: number}"},
		},
		{
			name: "a component prop of the wrong type",
			src: `fn MyComponent(props: {count: number}) {
	return <div>{props.count}</div>
}
val elem = <MyComponent count="five" />`,
			wantErrs: []string{`"five" cannot be assigned to number`},
		},
		{
			name:     "an unknown component",
			src:      `val elem = <UnknownComponent />`,
			wantErrs: []string{"Component 'UnknownComponent' is not defined"},
		},
		{
			name: "a child of the wrong type",
			src: `fn Container(props: {children: string}) {
	return <div>{props.children}</div>
}
val elem = <Container>{42}</Container>`,
			wantErrs: []string{"42 cannot be assigned to string"},
		},
		{
			name: "children passed to a component without a children prop",
			src: `fn Container(props: {title: string}) {
	return <div>{props.title}</div>
}
val elem = <Container title="Title">Unexpected child</Container>`,
			wantErrs: []string{"Component 'Container' does not accept children"},
		},
		{
			name:     "a key of the wrong type",
			src:      `val elem = <div key={true} />`,
			wantErrs: []string{"Invalid 'key' prop type: expected string | number | null, got true"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := InferScriptAgainstStdlib(parseScript(t, test.src), benchTree, Options{})
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Message()
			}
			if test.wantErrs == nil {
				require.Empty(t, msgs)
				return
			}
			require.Equal(t, test.wantErrs, msgs)
		})
	}
}
