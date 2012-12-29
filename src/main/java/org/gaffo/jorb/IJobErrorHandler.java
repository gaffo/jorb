package org.gaffo.jorb;

public interface IJobErrorHandler
{
    void handleException(Throwable e, ACJ job);
}