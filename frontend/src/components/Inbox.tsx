export default function Inbox() {
  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-4">Inbox</h2>
      <p className="text-gray-600">Review and process incoming documents.</p>
      <div className="mt-6 border-2 border-dashed border-gray-300 rounded-lg p-8 text-center text-gray-500">
        No documents to review
      </div>
    </div>
  )
}