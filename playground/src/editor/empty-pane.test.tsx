// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';

import { EmptyPane } from './empty-pane';

afterEach(cleanup);

const noDrop = () => ({
    onDragOver: vi.fn(),
    onDragLeave: vi.fn(),
    onDrop: vi.fn(),
});

test.each([
    {
        isFocused: true,
        text: 'Open a file from the explorer or drag a tab here',
    },
    {
        isFocused: false,
        text: 'Click to open files here, or drag a tab here',
    },
])('with isFocused $isFocused it reads "$text"', ({ isFocused, text }) => {
    render(
        <EmptyPane
            side="left"
            isFocused={isFocused}
            isDropTarget={false}
            dropHandlers={noDrop()}
            onFocus={vi.fn()}
        />,
    );

    expect(screen.getByRole('button')).toHaveTextContent(text);
});

test('a click calls onFocus', () => {
    const onFocus = vi.fn();
    render(
        <EmptyPane
            side="right"
            isFocused={false}
            isDropTarget={false}
            dropHandlers={noDrop()}
            onFocus={onFocus}
        />,
    );

    fireEvent.click(screen.getByRole('button'));

    expect(onFocus).toHaveBeenCalledOnce();
});

test('marks the pane while a tab is over it and passes drags to dropHandlers', () => {
    const dropHandlers = noDrop();
    render(
        <EmptyPane
            side="right"
            isFocused={false}
            isDropTarget={true}
            dropHandlers={dropHandlers}
            onFocus={vi.fn()}
        />,
    );
    const pane = screen.getByRole('button');

    fireEvent.dragOver(pane);
    fireEvent.drop(pane);

    expect(pane.className).toContain('dropTarget');
    expect(dropHandlers.onDragOver).toHaveBeenCalledOnce();
    expect(dropHandlers.onDrop).toHaveBeenCalledOnce();
});
