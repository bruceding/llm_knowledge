export default function ImportView() {
  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-4">Import</h2>
      <p className="text-gray-600 mb-6">Upload and import new documents into your knowledge base.</p>
      <div className="border-2 border-dashed border-gray-300 rounded-lg p-12 text-center">
        <div className="text-gray-400 mb-4">
          <svg
            className="mx-auto h-12 w-12"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={1.5}
              d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12"
            />
          </svg>
        </div>
        <p className="text-gray-600 mb-2">Drag and drop files here, or click to browse</p>
        <p className="text-sm text-gray-400">Supports PDF, TXT, MD, and more</p>
        <button className="mt-4 px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors">
          Select Files
        </button>
      </div>
    </div>
  )
}