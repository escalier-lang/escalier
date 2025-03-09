import ReactDOM from 'react-dom/client';
import { Editor } from './editor';
import './user-worker';

const root = document.getElementById('root');

if (!root) {
    throw new Error('Root element not found');
}
ReactDOM.createRoot(root).render(<Editor />);
