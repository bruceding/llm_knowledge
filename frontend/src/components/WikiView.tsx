export default function WikiView() {
  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-4">Wiki</h2>
      <p className="text-gray-600">Browse and search your knowledge base.</p>
      <div className="mt-6 grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        <div className="bg-gray-50 rounded-lg p-4 hover:bg-gray-100 cursor-pointer transition-colors">
          <h3 className="font-medium text-gray-800">Example Page 1</h3>
          <p className="text-sm text-gray-500 mt-1">Description of the wiki page...</p>
        </div>
        <div className="bg-gray-50 rounded-lg p-4 hover:bg-gray-100 cursor-pointer transition-colors">
          <h3 className="font-medium text-gray-800">Example Page 2</h3>
          <p className="text-sm text-gray-500 mt-1">Description of the wiki page...</p>
        </div>
      </div>
    </div>
  )
}