import { useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'

/**
 * useCapabilities reports what the engine accepts: formats, OCR engines,
 * document kinds. Cached for a while because it only changes on deploy.
 */
export function useCapabilities() {
  return useQuery({
    queryKey: ['capabilities'],
    queryFn: () => api.capabilities(),
    staleTime: 5 * 60 * 1000,
    select: (payload) => payload?.data,
  })
}

/**
 * useActiveJobs polls the work queue while anything is running and backs off to
 * a slow heartbeat when idle, so an open tab does not hammer the API.
 */
export function useActiveJobs({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['jobs', 'active'],
    queryFn: () => api.getJobs({ active: true, page_size: 25 }),
    enabled,
    select: (payload) => payload?.data ?? [],
    refetchInterval: (query) => {
      const jobs = query.state.data?.data ?? []
      return jobs.length > 0 ? 2000 : 15000
    },
  })
}

/**
 * useJobTracker follows one job to completion and refreshes the views its
 * outcome affects, which is what makes the UI update itself after an ingest
 * finishes instead of needing a manual reload.
 */
export function useJobTracker(jobId, { onFinished, invalidate = [] } = {}) {
  const queryClient = useQueryClient()
  const notified = useRef(false)

  const query = useQuery({
    queryKey: ['job', jobId],
    queryFn: () => api.getJob(jobId),
    enabled: Boolean(jobId),
    select: (payload) => payload?.data?.job,
    refetchInterval: (q) => {
      const job = q.state.data?.data?.job
      if (!job) return 2000
      return ['completed', 'failed', 'cancelled'].includes(job.status) ? false : 1500
    },
  })

  const job = query.data
  const finished = job && ['completed', 'failed', 'cancelled'].includes(job.status)

  useEffect(() => {
    if (!finished || notified.current) return
    notified.current = true
    invalidate.forEach((key) => queryClient.invalidateQueries({ queryKey: key }))
    queryClient.invalidateQueries({ queryKey: ['jobs'] })
    queryClient.invalidateQueries({ queryKey: ['overview'] })
    if (onFinished) onFinished(job)
  }, [finished, job, invalidate, onFinished, queryClient])

  useEffect(() => {
    notified.current = false
  }, [jobId])

  return { job, finished, ...query }
}

/** useOverview backs the dashboard. */
export function useOverview() {
  return useQuery({
    queryKey: ['overview'],
    queryFn: () => api.overview(),
    select: (payload) => payload?.data,
    refetchInterval: 10000,
  })
}

/** useConverterHealth shows whether document processing is available at all. */
export function useConverterHealth() {
  return useQuery({
    queryKey: ['converter-health'],
    queryFn: () => api.converterHealth(),
    select: (payload) => payload?.data,
    refetchInterval: 30000,
    retry: false,
  })
}

/** useExamOptions is the exam picker used by several forms. */
export function useExamOptions() {
  return useQuery({
    queryKey: ['exams', 'options'],
    queryFn: () => api.getExams({ page_size: 200 }),
    select: (payload) => payload?.data ?? [],
    staleTime: 30 * 1000,
  })
}

/** useSubjectOptions is the subject picker, with question counts. */
export function useSubjectOptions() {
  return useQuery({
    queryKey: ['subjects', 'options'],
    queryFn: () => api.getSubjects({ with_topics: false, with_counts: true }),
    select: (payload) => payload?.data ?? [],
    staleTime: 30 * 1000,
  })
}

/**
 * useReviewSummary backs the review queue header and the dashboard's quality
 * panel. It refreshes on a slow interval because audit jobs change it in the
 * background.
 */
export function useReviewSummary() {
  return useQuery({
    queryKey: ['review', 'summary'],
    queryFn: () => api.getReviewSummary(),
    select: (payload) => payload?.data,
    refetchInterval: 15000,
  })
}

/** useModelStatus tells the UI whether model review is configured and reachable. */
export function useModelStatus() {
  return useQuery({
    queryKey: ['model-status'],
    queryFn: () => api.modelStatus(),
    select: (payload) => payload?.data,
    staleTime: 60 * 1000,
    retry: false,
  })
}

/** usePaperQA reads a paper's quality report. */
export function usePaperQA(paperId) {
  return useQuery({
    queryKey: ['paper', paperId, 'qa'],
    queryFn: () => api.getPaperQA(paperId),
    enabled: Boolean(paperId),
    select: (payload) => payload?.data,
  })
}

/** useProvenance loads where a question came from and how it was processed. */
export function useProvenance(questionId) {
  return useQuery({
    queryKey: ['question', questionId, 'provenance'],
    queryFn: () => api.getProvenance(questionId),
    enabled: Boolean(questionId),
    select: (payload) => payload?.data,
  })
}
