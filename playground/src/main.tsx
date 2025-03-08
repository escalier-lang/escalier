import ReactDOM from 'react-dom/client';
import { Editor } from './editor'; 
import './user-worker';

ReactDOM.createRoot(document.getElementById('root')!).render(
	<Editor />
);
