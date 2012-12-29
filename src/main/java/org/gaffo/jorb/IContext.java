package org.gaffo.jorb;

public interface IContext
{
    <T> T get(Class<T> clazz);
    void register(Object obj);
}
