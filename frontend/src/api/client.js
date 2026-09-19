import axios from 'axios'

const API_BASE_URL = import.meta.env.VITE_API_URL || '/api/v1'
const API_KEY = import.meta.env.VITE_API_KEY || ''

const apiClient = axios.create({
  baseURL: API_BASE_URL,
  headers: { 'Content-Type': 'application/json' },
})

if (API_KEY) {
  apiClient.defaults.headers.common['X-API-Key'] = API_KEY
}

// The API answers {data, meta}; unwrap the axios envelope and keep ours.
apiClient.interceptors.response.use(
  (response) => response.data,
  (error) => {
    const payload = error.response?.data
    const message = payload?.error || error.message || 'Something went wrong'
    const wrapped = new Error(message)
    wrapped.status = error.response?.status
    // Some failures carry useful structure, such as a paper availability report
    // explaining exactly which subject is short.
    wrapped.details = payload
    return Promise.reject(wrapped)
  }
)

// clean strips empty values so filters do not send noise like ?status=
const clean = (params = {}) =>
  Object.fromEntries(
    Object.entries(params).filter(
      ([, value]) => value !== undefined && value !== null && value !== '' && value !== 'all'
    )
  )

export const api = {
  // --- meta ---------------------------------------------------------------
  capabilities: () => apiClient.get('/meta/capabilities'),
  overview: () => apiClient.get('/meta/overview'),
  converterHealth: () => apiClient.get('/meta/converter'),
  modelStatus: () => apiClient.get('/meta/model'),

  // --- review -------------------------------------------------------------
  // Everything not cleared for delivery, plus the decisions taken about it.
  getReviewQueue: (params) => apiClient.get('/review', { params: clean(params) }),
  getReviewSummary: () => apiClient.get('/review/summary'),
  requalify: (body) => apiClient.post('/review/requalify', body || {}),
  getProvenance: (id) => apiClient.get(`/questions/${id}/provenance`),
  resolveQuestion: (id, body) => apiClient.post(`/questions/${id}/resolve`, body),

  // --- exams --------------------------------------------------------------
  getExams: (params) => apiClient.get('/exams', { params: clean(params) }),
  createExam: (body) => apiClient.post('/exams', body),
  getExam: (id) => apiClient.get(`/exams/${id}`),
  updateExam: (id, body) => apiClient.put(`/exams/${id}`, body),
  deleteExam: (id) => apiClient.delete(`/exams/${id}`),

  getExamSubjects: (id) => apiClient.get(`/exams/${id}/subjects`),
  addExamSubject: (id, body) => apiClient.post(`/exams/${id}/subjects`, body),
  removeExamSubject: (id, subjectId) => apiClient.delete(`/exams/${id}/subjects/${subjectId}`),

  // --- patterns -----------------------------------------------------------
  getPatterns: (examId) => apiClient.get(`/exams/${examId}/patterns`),
  getPattern: (examId, patternId) => apiClient.get(`/exams/${examId}/patterns/${patternId}`),
  createPattern: (examId, body) => apiClient.post(`/exams/${examId}/patterns`, body),
  updatePattern: (examId, patternId, body) =>
    apiClient.put(`/exams/${examId}/patterns/${patternId}`, body),
  activatePattern: (examId, patternId) =>
    apiClient.post(`/exams/${examId}/patterns/${patternId}/activate`),
  deletePattern: (examId, patternId) =>
    apiClient.delete(`/exams/${examId}/patterns/${patternId}`),
  // Derive a pattern from real question papers.
  analyzePattern: (examId, body) => apiClient.post(`/exams/${examId}/pattern-analysis`, body),

  // --- associations -------------------------------------------------------
  getAssociations: (examId) => apiClient.get(`/exams/${examId}/associations`),
  createAssociation: (examId, body) => apiClient.post(`/exams/${examId}/associations`, body),
  updateAssociation: (examId, assocId, body) =>
    apiClient.put(`/exams/${examId}/associations/${assocId}`, body),
  deleteAssociation: (examId, assocId) =>
    apiClient.delete(`/exams/${examId}/associations/${assocId}`),

  // --- taxonomy -----------------------------------------------------------
  getSubjects: (params) => apiClient.get('/subjects', { params: clean(params) }),
  createSubject: (body) => apiClient.post('/subjects', body),
  getSubject: (id) => apiClient.get(`/subjects/${id}`),
  updateSubject: (id, body) => apiClient.put(`/subjects/${id}`, body),
  deleteSubject: (id) => apiClient.delete(`/subjects/${id}`),

  getTopics: (params) => apiClient.get('/topics', { params: clean(params) }),
  createTopic: (body) => apiClient.post('/topics', body),
  updateTopic: (id, body) => apiClient.put(`/topics/${id}`, body),
  deleteTopic: (id) => apiClient.delete(`/topics/${id}`),

  // --- documents ----------------------------------------------------------
  getDocuments: (params) => apiClient.get('/documents', { params: clean(params) }),
  getDocument: (id) => apiClient.get(`/documents/${id}`),
  updateDocument: (id, body) => apiClient.patch(`/documents/${id}`, body),
  deleteDocument: (id, params) =>
    apiClient.delete(`/documents/${id}`, { params: clean(params) }),
  getConversion: (id, params) =>
    apiClient.get(`/documents/${id}/conversion`, { params: clean(params) }),
  getBlocks: (id, params) => apiClient.get(`/documents/${id}/blocks`, { params: clean(params) }),
  ingestDocument: (id, body) => apiClient.post(`/documents/${id}/ingest`, body || {}),
  reparseDocument: (id) => apiClient.post(`/documents/${id}/reparse`),
  downloadUrl: (id) => `${API_BASE_URL}/documents/${id}/download`,

  /**
   * Upload one or more files. The browser sets the multipart boundary, so the
   * JSON content type has to be removed for this request only.
   */
  uploadDocuments: ({ files, kind, title, examId, year, language, ocrMode, ocrEngine, autoIngest }, onProgress) => {
    const form = new FormData()
    Array.from(files).forEach((file) => form.append('files', file))
    if (kind) form.append('kind', kind)
    if (title) form.append('title', title)
    if (examId) form.append('exam_id', String(examId))
    if (year) form.append('year', String(year))
    if (language) form.append('language', language)
    if (ocrMode) form.append('ocr_mode', ocrMode)
    if (ocrEngine) form.append('ocr_engine', ocrEngine)
    form.append('auto_ingest', autoIngest === false ? 'false' : 'true')

    return apiClient.post('/documents', form, {
      headers: { 'Content-Type': undefined },
      onUploadProgress: (event) => {
        if (!onProgress || !event.total) return
        onProgress(Math.round((event.loaded * 100) / event.total))
      },
    })
  },

  // --- jobs ---------------------------------------------------------------
  getJobs: (params) => apiClient.get('/jobs', { params: clean(params) }),
  getJob: (id) => apiClient.get(`/jobs/${id}`),
  cancelJob: (id) => apiClient.post(`/jobs/${id}/cancel`),
  retryJob: (id) => apiClient.post(`/jobs/${id}/retry`),

  // --- warehouse ----------------------------------------------------------
  getWarehouse: (params) => apiClient.get('/warehouse', { params: clean(params) }),
  getQuestions: (params) => apiClient.get('/questions', { params: clean(params) }),
  getQuestion: (id) => apiClient.get(`/questions/${id}`),
  createQuestion: (body) => apiClient.post('/questions', body),
  updateQuestion: (id, body) => apiClient.put(`/questions/${id}`, body),
  deleteQuestion: (id, params) =>
    apiClient.delete(`/questions/${id}`, { params: clean(params) }),
  reviewQuestion: (id, body) => apiClient.post(`/questions/${id}/review`, body),
  bulkUpdateQuestions: (body) => apiClient.patch('/questions', body),

  // --- papers -------------------------------------------------------------
  getPapers: (params) => apiClient.get('/papers', { params: clean(params) }),
  getPaper: (id) => apiClient.get(`/papers/${id}`),
  updatePaper: (id, body) => apiClient.put(`/papers/${id}`, body),
  deletePaper: (id) => apiClient.delete(`/papers/${id}`),
  paperAvailability: (examId, body) =>
    apiClient.post(`/exams/${examId}/paper-availability`, body || {}),
  generatePaper: (examId, body) => apiClient.post(`/exams/${examId}/papers`, body || {}),
  getPaperQA: (id) => apiClient.get(`/papers/${id}/qa`),
  runPaperQA: (id, params) => apiClient.post(`/papers/${id}/qa`, null, { params: clean(params) }),
  // draft=true is required to export a paper that has not passed QA, and the
  // export is stamped as a draft when it has not.
  exportUrl: (id, { answers = true, explanations = true, draft = false } = {}) =>
    `${API_BASE_URL}/papers/${id}/export?answers=${answers}&explanations=${explanations}${draft ? '&draft=true' : ''}`,
}

export default apiClient
