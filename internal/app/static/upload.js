class FileUploader {
    constructor() {
        this.files = new Set();
        this.init();
    }

    init() {
        this.uploadArea = document.getElementById('upload-area');
        this.fileInput = document.getElementById('file-input');
        this.fileList = document.getElementById('file-list');
        this.uploadBtn = document.getElementById('upload-btn');
        this.clearBtn = document.getElementById('clear-btn');
        this.results = document.getElementById('results');
        this.expiresInput = document.getElementById('expires');

        this.bindEvents();
    }

    bindEvents() {
        // File input change
        this.fileInput.addEventListener('change', (e) => {
            this.addFiles(Array.from(e.target.files));
            e.target.value = ''; // Reset input
        });

        // Drag and drop
        this.uploadArea.addEventListener('dragover', (e) => {
            e.preventDefault();
            this.uploadArea.classList.add('drag-over');
        });

        this.uploadArea.addEventListener('dragleave', (e) => {
            e.preventDefault();
            this.uploadArea.classList.remove('drag-over');
        });

        this.uploadArea.addEventListener('drop', (e) => {
            e.preventDefault();
            this.uploadArea.classList.remove('drag-over');
            this.addFiles(Array.from(e.dataTransfer.files));
        });

        // Paste functionality
        document.addEventListener('paste', (e) => {
            const items = e.clipboardData.items;
            const files = [];
            
            for (let item of items) {
                if (item.kind === 'file') {
                    files.push(item.getAsFile());
                }
            }
            
            if (files.length > 0) {
                e.preventDefault();
                this.addFiles(files);
            }
        });

        // Upload button
        this.uploadBtn.addEventListener('click', () => {
            this.uploadFiles();
        });

        // Clear button
        this.clearBtn.addEventListener('click', () => {
            this.clearFiles();
        });
    }

    addFiles(fileArray) {
        fileArray.forEach(file => {
            if (!this.isDuplicate(file)) {
                this.files.add(file);
            }
        });
        this.updateFileList();
        this.updateUploadButton();
    }

    isDuplicate(file) {
        for (let existingFile of this.files) {
            if (existingFile.name === file.name && 
                existingFile.size === file.size && 
                existingFile.lastModified === file.lastModified) {
                return true;
            }
        }
        return false;
    }

    removeFile(file) {
        this.files.delete(file);
        this.updateFileList();
        this.updateUploadButton();
    }

    clearFiles() {
        this.files.clear();
        this.updateFileList();
        this.updateUploadButton();
        this.hideResults();
    }

    updateFileList() {
        this.fileList.innerHTML = '';
        
        this.files.forEach(file => {
            const fileItem = document.createElement('div');
            fileItem.className = 'file-item';
            
            const fileName = document.createElement('span');
            fileName.textContent = `${file.name} (${this.formatFileSize(file.size)})`;
            
            const removeBtn = document.createElement('button');
            removeBtn.textContent = 'Remove';
            removeBtn.onclick = () => this.removeFile(file);
            
            fileItem.appendChild(fileName);
            fileItem.appendChild(removeBtn);
            this.fileList.appendChild(fileItem);
        });
    }

    updateUploadButton() {
        this.uploadBtn.disabled = this.files.size === 0;
    }

    formatFileSize(bytes) {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    showResults(text) {
        console.log('Showing results:', text);
        this.results.innerHTML = text;
        this.results.classList.add('show');
    }

    hideResults() {
        this.results.classList.remove('show');
        this.results.innerHTML = '';
    }

    async uploadFiles() {
        if (this.files.size === 0) return;

        console.log('Starting upload for', this.files.size, 'files');
        this.uploadBtn.disabled = true;
        this.showResults('<div class="uploading">Uploading files...</div>');

        const expires = this.expiresInput.value;
        const results = [];

        for (let file of this.files) {
            console.log('Uploading file:', file.name);
            try {
                const formData = new FormData();
                formData.append('file', file);
                if (expires) {
                    formData.append('expires', expires);
                }

                console.log('Making fetch request...');
                const response = await fetch('/', {
                    method: 'POST',
                    body: formData
                });

                console.log('Response status:', response.status);
                if (response.ok) {
                    const url = await response.text();
                    const cleanUrl = url.trim();
                    console.log('Upload successful, URL:', cleanUrl);
                    results.push(`<div class="success">✓ ${file.name}:<br><a href="${cleanUrl}" target="_blank">${cleanUrl}</a></div>`);
                } else {
                    console.log('Upload failed with status:', response.status);
                    results.push(`<div class="error">✗ ${file.name}: Upload failed (${response.status})</div>`);
                }
            } catch (error) {
                console.log('Upload error:', error);
                results.push(`<div class="error">✗ ${file.name}: ${error.message}</div>`);
            }
        }

        console.log('Final results:', results);
        this.showResults(results.join(''));
        this.clearFiles();
        this.uploadBtn.disabled = false;
    }
}

// Initialize when page loads
document.addEventListener('DOMContentLoaded', () => {
    new FileUploader();
});