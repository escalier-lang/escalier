import ReactDOM from 'react-dom/client';
import { Editor } from './editor'; 
import './languages/escalier/monaco.contribution';
import './user-worker';

ReactDOM.createRoot(document.getElementById('root')!).render(
	<Editor />
);
