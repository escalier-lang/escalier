import { useCallback, useEffect, useRef, useState } from 'react';

import type { BrowserFS } from '../fs/browser-fs';
import type { FSDir, FSNode } from '../fs/fs-node';
import { ConfirmDialog } from './confirm-dialog';
import styles from './file-explorer.module.css';

type FileExplorerProps = {
    fs: BrowserFS;
    onFileOpen: (path: string) => void;
    onFileDelete?: (path: string) => void;
    onFileRename?: (oldPath: string, newPath: string) => void;
};

type ContextMenuState = {
    path: string;
    nodeType: 'file' | 'dir';
    x: number;
    y: number;
};

type InlineInputState =
    | {
          type: 'create';
          /** Parent directory path where the new item will be created */
          parentPath: string;
          kind: 'file' | 'dir';
      }
    | {
          type: 'rename';
          /** Path of the node being renamed */
          renamePath: string;
          /** Current name (pre-filled in the input) */
          currentName: string;
      };

type DeleteConfirmState = {
    path: string;
    name: string;
    kind: 'file' | 'dir';
};

/** Directories/files to hide from the explorer */
function isHidden(name: string): boolean {
    return name === '.pnpm';
}

/** Whether CRUD operations should be disabled and entries dimmed for this path. */
function isProtected(path: string): boolean {
    return (
        path === '/build' ||
        path.startsWith('/build/') ||
        path === '/node_modules' ||
        path.startsWith('/node_modules/') ||
        /^\/packages\/[^/]+\/(build|node_modules)(\/|$)/.test(path)
    );
}

/** Root parent path — used by header buttons to create items at the project root. */
const ROOT_PATH = '';

export const FileExplorer = ({
    fs,
    onFileOpen,
    onFileDelete,
    onFileRename,
}: FileExplorerProps) => {
    const [, setRev] = useState(0);
    const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(
        null,
    );
    const [inlineInput, setInlineInput] = useState<InlineInputState | null>(
        null,
    );
    const [deleteConfirm, setDeleteConfirm] =
        useState<DeleteConfirmState | null>(null);
    // Tracks explicit expand/collapse overrides. `true` = expanded, `false` = collapsed.
    // Directories not in this map use their default state (collapsed for
    // node_modules/build, expanded for everything else).
    const [expandOverrides, setExpandOverrides] = useState<
        Map<string, boolean>
    >(() => new Map());

    // Re-render when FS changes
    useEffect(() => {
        const listener = () => setRev((r) => r + 1);
        fs.events.on(listener);
        return () => fs.events.off(listener);
    }, [fs]);

    const contextMenuRef = useRef<HTMLDivElement>(null);

    // Dismiss context menu on click/pointerdown elsewhere
    useEffect(() => {
        if (!contextMenu) return;
        const dismiss = () => setContextMenu(null);
        window.addEventListener('click', dismiss);
        window.addEventListener('pointerdown', dismiss);
        return () => {
            window.removeEventListener('click', dismiss);
            window.removeEventListener('pointerdown', dismiss);
        };
    }, [contextMenu]);

    // Auto-focus the first menu item when the context menu opens
    useEffect(() => {
        if (!contextMenu) return;
        const firstButton =
            contextMenuRef.current?.querySelector<HTMLElement>('button');
        firstButton?.focus();
    }, [contextMenu]);

    const handleContextMenu = useCallback(
        (path: string, nodeType: 'file' | 'dir', x: number, y: number) => {
            setContextMenu({ path, nodeType, x, y });
        },
        [],
    );

    const expandDir = useCallback((dirPath: string) => {
        setExpandOverrides((prev) => new Map(prev).set(dirPath, true));
    }, []);

    const handleNewFile = useCallback(
        (parentPath: string) => {
            setContextMenu(null);
            expandDir(parentPath);
            setInlineInput({ type: 'create', parentPath, kind: 'file' });
        },
        [expandDir],
    );

    const handleNewFolder = useCallback(
        (parentPath: string) => {
            setContextMenu(null);
            expandDir(parentPath);
            setInlineInput({ type: 'create', parentPath, kind: 'dir' });
        },
        [expandDir],
    );

    const handleRename = useCallback((path: string) => {
        setContextMenu(null);
        const name = path.split('/').pop() ?? '';
        setInlineInput({ type: 'rename', renamePath: path, currentName: name });
    }, []);

    const handleDelete = useCallback((path: string, kind: 'file' | 'dir') => {
        setContextMenu(null);
        const name = path.split('/').pop() ?? path;
        setDeleteConfirm({ path, name, kind });
    }, []);

    const handleInlineSubmit = useCallback(
        (name: string) => {
            if (!inlineInput || !name.trim()) {
                setInlineInput(null);
                return;
            }

            const trimmed = name.trim();
            if (
                trimmed === '.' ||
                trimmed === '..' ||
                trimmed.includes('/') ||
                trimmed.includes('\\')
            ) {
                setInlineInput(null);
                return;
            }

            if (inlineInput.type === 'rename') {
                const oldPath = inlineInput.renamePath;
                const parentPath = oldPath.substring(
                    0,
                    oldPath.lastIndexOf('/'),
                );
                const newPath = `${parentPath}/${trimmed}`;
                if (newPath === oldPath) {
                    setInlineInput(null);
                    return;
                }
                fs.rename(oldPath, newPath, (err) => {
                    if (err) return;
                    onFileRename?.(oldPath, newPath);
                    setInlineInput(null);
                });
            } else {
                const fullPath = `${inlineInput.parentPath}/${trimmed}`;
                if (inlineInput.kind === 'dir') {
                    fs.mkdir(fullPath, (err) => {
                        if (err) return;
                        setInlineInput(null);
                    });
                } else {
                    const content = new TextEncoder().encode('');
                    fs.writeFile(fullPath, content, (err) => {
                        if (err) return;
                        onFileOpen(fullPath);
                        setInlineInput(null);
                    });
                }
            }
        },
        [inlineInput, fs, onFileOpen, onFileRename],
    );

    const handleDeleteConfirm = useCallback(() => {
        if (!deleteConfirm) return;
        const { path, kind } = deleteConfirm;
        const remove = kind === 'dir' ? fs.rmdir.bind(fs) : fs.unlink.bind(fs);
        remove(path, (err) => {
            if (err) return;
            onFileDelete?.(path);
            setDeleteConfirm(null);
        });
    }, [deleteConfirm, fs, onFileDelete]);

    return (
        <div className={styles.explorer}>
            <div className={styles.header}>
                <span>EXPLORER</span>
                <div className={styles.headerActions}>
                    <button
                        type="button"
                        className={styles.headerButton}
                        onClick={() => handleNewFile(ROOT_PATH)}
                        aria-label="New File"
                        title="New File"
                    >
                        +
                    </button>
                    <button
                        type="button"
                        className={styles.headerButton}
                        onClick={() => handleNewFolder(ROOT_PATH)}
                        aria-label="New Folder"
                        title="New Folder"
                    >
                        <svg
                            width="14"
                            height="14"
                            viewBox="0 0 16 16"
                            fill="currentColor"
                            aria-hidden="true"
                        >
                            <path d="M14 4H8.618l-1-2H2a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V5a1 1 0 0 0-1-1z" />
                        </svg>
                    </button>
                </div>
            </div>
            <div className={styles.tree}>
                <DirChildren
                    dir={fs.rootDir}
                    parentPath=""
                    onFileClick={onFileOpen}
                    onContextMenu={handleContextMenu}
                    inlineInput={inlineInput}
                    onInlineSubmit={handleInlineSubmit}
                    onInlineCancel={() => setInlineInput(null)}
                    expandOverrides={expandOverrides}
                    onToggleExpand={(path, expanded) => {
                        setExpandOverrides((prev) =>
                            new Map(prev).set(path, expanded),
                        );
                    }}
                />
            </div>

            {/* Context menu */}
            {contextMenu && (
                <div
                    ref={contextMenuRef}
                    className={styles.contextMenu}
                    style={{ left: contextMenu.x, top: contextMenu.y }}
                    role="menu"
                    onClick={(e) => e.stopPropagation()}
                    onPointerDown={(e) => e.stopPropagation()}
                    onKeyDown={(e) => {
                        e.stopPropagation();
                        if (e.key === 'Escape') {
                            setContextMenu(null);
                        } else if (
                            e.key === 'ArrowDown' ||
                            e.key === 'ArrowUp'
                        ) {
                            e.preventDefault();
                            const buttons =
                                contextMenuRef.current?.querySelectorAll<HTMLElement>(
                                    'button',
                                );
                            if (!buttons?.length) return;
                            const items = Array.from(buttons);
                            const idx = items.indexOf(
                                document.activeElement as HTMLElement,
                            );
                            const next =
                                e.key === 'ArrowDown'
                                    ? (idx + 1) % items.length
                                    : (idx - 1 + items.length) % items.length;
                            items[next].focus();
                        }
                    }}
                >
                    {contextMenu.nodeType === 'dir' &&
                        !isProtected(contextMenu.path) && (
                            <>
                                <button
                                    type="button"
                                    role="menuitem"
                                    className={styles.contextMenuItem}
                                    onClick={() =>
                                        handleNewFile(contextMenu.path)
                                    }
                                >
                                    New File
                                </button>
                                <button
                                    type="button"
                                    role="menuitem"
                                    className={styles.contextMenuItem}
                                    onClick={() =>
                                        handleNewFolder(contextMenu.path)
                                    }
                                >
                                    New Folder
                                </button>
                                <div className={styles.contextMenuSeparator} />
                            </>
                        )}
                    {!isProtected(contextMenu.path) && (
                        <>
                            <button
                                type="button"
                                role="menuitem"
                                className={styles.contextMenuItem}
                                onClick={() => handleRename(contextMenu.path)}
                            >
                                Rename
                            </button>
                            <button
                                type="button"
                                role="menuitem"
                                className={`${styles.contextMenuItem} ${styles.destructiveItem}`}
                                onClick={() =>
                                    handleDelete(
                                        contextMenu.path,
                                        contextMenu.nodeType,
                                    )
                                }
                            >
                                Delete
                            </button>
                        </>
                    )}
                </div>
            )}

            {/* Delete confirmation dialog */}
            {deleteConfirm && (
                <ConfirmDialog
                    title={`Delete ${deleteConfirm.kind === 'dir' ? 'folder' : 'file'}`}
                    message={`Are you sure you want to delete "${deleteConfirm.name}"? This action cannot be undone.`}
                    confirmLabel="Delete"
                    destructive
                    onConfirm={handleDeleteConfirm}
                    onCancel={() => setDeleteConfirm(null)}
                />
            )}
        </div>
    );
};

type DirChildrenProps = {
    dir: FSDir;
    parentPath: string;
    onFileClick: (path: string) => void;
    onContextMenu: (
        path: string,
        nodeType: 'file' | 'dir',
        x: number,
        y: number,
    ) => void;
    inlineInput: InlineInputState | null;
    onInlineSubmit: (name: string) => void;
    onInlineCancel: () => void;
    expandOverrides: Map<string, boolean>;
    onToggleExpand: (path: string, expanded: boolean) => void;
};

const DirChildren = ({
    dir,
    parentPath,
    onFileClick,
    onContextMenu,
    inlineInput,
    onInlineSubmit,
    onInlineCancel,
    expandOverrides,
    onToggleExpand,
}: DirChildrenProps) => {
    // Sort: directories first, then files, alphabetical within each group
    const entries = Array.from(dir.children.entries())
        .filter(([name]) => !isHidden(name))
        .sort(([aName, aNode], [bName, bNode]) => {
            const aIsDir = aNode.type === 'dir' ? 0 : 1;
            const bIsDir = bNode.type === 'dir' ? 0 : 1;
            if (aIsDir !== bIsDir) return aIsDir - bIsDir;
            return aName.localeCompare(bName);
        });

    // Show inline input for new file/folder creation in this directory
    const showInlineCreate =
        inlineInput &&
        inlineInput?.type === 'create' &&
        inlineInput.parentPath === parentPath;

    return (
        <ul className={styles.list}>
            {entries.map(([name, node]) => {
                const path = `${parentPath}/${name}`;
                return (
                    <TreeNode
                        key={name}
                        name={name}
                        node={node}
                        path={path}
                        onFileClick={onFileClick}
                        onContextMenu={onContextMenu}
                        inlineInput={inlineInput}
                        onInlineSubmit={onInlineSubmit}
                        onInlineCancel={onInlineCancel}
                        expandOverrides={expandOverrides}
                        onToggleExpand={onToggleExpand}
                    />
                );
            })}
            {showInlineCreate && (
                <li>
                    <InlineNameInput
                        initialValue=""
                        kind={inlineInput.kind}
                        onSubmit={onInlineSubmit}
                        onCancel={onInlineCancel}
                    />
                </li>
            )}
        </ul>
    );
};

type TreeNodeProps = {
    name: string;
    node: FSNode;
    path: string;
    onFileClick: (path: string) => void;
    onContextMenu: (
        path: string,
        nodeType: 'file' | 'dir',
        x: number,
        y: number,
    ) => void;
    inlineInput: InlineInputState | null;
    onInlineSubmit: (name: string) => void;
    onInlineCancel: () => void;
    expandOverrides: Map<string, boolean>;
    onToggleExpand: (path: string, expanded: boolean) => void;
};

const TreeNode = ({
    name,
    node,
    path,
    onFileClick,
    onContextMenu,
    inlineInput,
    onInlineSubmit,
    onInlineCancel,
    expandOverrides,
    onToggleExpand,
}: TreeNodeProps) => {
    const defaultExpanded = name !== 'node_modules' && name !== 'build';
    const isExpanded = expandOverrides.get(path) ?? defaultExpanded;
    const isDimmed = isProtected(path);

    const isRenaming =
        inlineInput &&
        inlineInput.type === 'rename' &&
        inlineInput.renamePath === path;

    const handleToggle = () => {
        onToggleExpand(path, !isExpanded);
    };

    const openMenuFromKeyboard = (
        e: React.KeyboardEvent,
        nodeType: 'file' | 'dir',
    ) => {
        if (e.key === 'F10' && e.shiftKey && !isProtected(path)) {
            e.preventDefault();
            const rect = e.currentTarget.getBoundingClientRect();
            onContextMenu(path, nodeType, rect.left, rect.bottom);
        }
    };

    if (node.type === 'dir') {
        const showMenu = !isProtected(path);
        return (
            <li>
                {isRenaming ? (
                    <InlineNameInput
                        initialValue={
                            inlineInput.type === 'rename'
                                ? inlineInput.currentName
                                : ''
                        }
                        kind="dir"
                        onSubmit={onInlineSubmit}
                        onCancel={onInlineCancel}
                    />
                ) : (
                    <button
                        type="button"
                        className={`${styles.entry} ${styles.dirEntry} ${isDimmed ? styles.dimmed : ''}`}
                        onClick={handleToggle}
                        onKeyDown={(e) => openMenuFromKeyboard(e, 'dir')}
                        onContextMenu={
                            showMenu
                                ? (e) => {
                                      e.preventDefault();
                                      onContextMenu(
                                          path,
                                          'dir',
                                          e.clientX,
                                          e.clientY,
                                      );
                                  }
                                : undefined
                        }
                        aria-expanded={isExpanded}
                    >
                        <span className={styles.chevron}>
                            {isExpanded ? '\u25BE' : '\u25B8'}
                        </span>
                        {name}
                    </button>
                )}
                {isExpanded && (
                    <DirChildren
                        dir={node}
                        parentPath={path}
                        onFileClick={onFileClick}
                        onContextMenu={onContextMenu}
                        inlineInput={inlineInput}
                        onInlineSubmit={onInlineSubmit}
                        onInlineCancel={onInlineCancel}
                        expandOverrides={expandOverrides}
                        onToggleExpand={onToggleExpand}
                    />
                )}
            </li>
        );
    }

    if (node.type === 'file') {
        const showMenu = !isProtected(path);
        return (
            <li>
                {isRenaming ? (
                    <InlineNameInput
                        initialValue={
                            inlineInput.type === 'rename'
                                ? inlineInput.currentName
                                : ''
                        }
                        kind="file"
                        onSubmit={onInlineSubmit}
                        onCancel={onInlineCancel}
                    />
                ) : (
                    <button
                        type="button"
                        className={`${styles.entry} ${styles.fileEntry} ${isDimmed ? styles.dimmed : ''}`}
                        onClick={() => onFileClick(path)}
                        onKeyDown={(e) => openMenuFromKeyboard(e, 'file')}
                        onContextMenu={
                            showMenu
                                ? (e) => {
                                      e.preventDefault();
                                      onContextMenu(
                                          path,
                                          'file',
                                          e.clientX,
                                          e.clientY,
                                      );
                                  }
                                : undefined
                        }
                    >
                        {name}
                    </button>
                )}
            </li>
        );
    }

    // symlinks - show but don't make interactive
    return (
        <li>
            <div className={`${styles.entry} ${styles.dimmed}`}>
                {name} &rarr;
            </div>
        </li>
    );
};

type InlineNameInputProps = {
    initialValue: string;
    kind: 'file' | 'dir';
    onSubmit: (name: string) => void;
    onCancel: () => void;
};

const InlineNameInput = ({
    initialValue,
    kind,
    onSubmit,
    onCancel,
}: InlineNameInputProps) => {
    const inputRef = useRef<HTMLInputElement>(null);
    const committedRef = useRef(false);

    useEffect(() => {
        const input = inputRef.current;
        if (!input) return;
        input.focus();
        // Select the name part before the extension for files
        if (initialValue && kind === 'file') {
            const dotIndex = initialValue.lastIndexOf('.');
            if (dotIndex > 0) {
                input.setSelectionRange(0, dotIndex);
            } else {
                input.select();
            }
        } else if (initialValue) {
            input.select();
        }
    }, [initialValue, kind]);

    const commit = (value: string) => {
        if (committedRef.current) return;
        committedRef.current = true;
        onSubmit(value);
        // If the component is still mounted after onSubmit (e.g. the FS
        // operation failed and the input wasn't dismissed), reset so the
        // user can retry or cancel.
        queueMicrotask(() => {
            committedRef.current = false;
        });
    };

    const cancel = () => {
        if (committedRef.current) return;
        committedRef.current = true;
        onCancel();
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter') {
            e.preventDefault();
            commit(e.currentTarget.value);
        } else if (e.key === 'Escape') {
            e.preventDefault();
            cancel();
        }
    };

    return (
        <input
            ref={inputRef}
            className={`${styles.inlineInput} ${kind === 'dir' ? styles.dirEntry : styles.fileEntry}`}
            defaultValue={initialValue}
            onKeyDown={handleKeyDown}
            onBlur={(e) => {
                const value = e.currentTarget.value.trim();
                if (value && value !== initialValue) {
                    commit(value);
                } else {
                    cancel();
                }
            }}
            aria-label={
                initialValue ? `Rename ${initialValue}` : `New ${kind} name`
            }
        />
    );
};
